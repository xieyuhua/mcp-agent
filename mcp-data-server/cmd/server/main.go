package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/admin"
	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/handler"
	"company.com/mcp-data-server/internal/mask"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/service"
	"company.com/mcp-data-server/internal/web"

	"github.com/gin-gonic/gin"
)

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径（为空则用环境变量/内置默认值，便于作为子进程直接运行）")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db, err := repository.OpenDB(cfg)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	if err := repository.AutoMigrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if cfg.SeedDemo {
		if err := repository.Seed(db); err != nil {
			log.Fatalf("seed: %v", err)
		}
	}

	roleRepo := repository.NewRoleRepo(db)
	if err := roleRepo.SeedBuiltinRoles("system"); err != nil {
		log.Printf("warn: seed builtin roles: %v", err)
	}

	authSvc := service.NewAuthService(db, cfg.JWTSecret)
	auditSvc := service.NewAuditService(db)
	queryRepo := repository.NewQueryRepo(db)

	permRepo := repository.NewPermissionRepo(db)
	authz := auth.NewResolver(permRepo)
	masker := mask.NewResolver(permRepo)
	if err := authz.Refresh(""); err != nil {
		log.Printf("warn: warm auth cache: %v", err)
	}
	if err := masker.Refresh(""); err != nil {
		log.Printf("warn: warm mask cache: %v", err)
	}

	querySvc := service.NewQueryService(queryRepo, auditSvc, authz, masker, cfg.MaskEnabled)
	permSvc := service.NewPermissionService(permRepo, roleRepo, authz, masker, auditSvc)
	webSvc := service.NewWebService(cfg.SearchProvider)
	weatherSvc := service.NewWeatherService()

	toolHandler := handler.NewToolHandler(authSvc, querySvc, permSvc, webSvc, weatherSvc, cfg.WorkDir, cfg.SandboxEnabled)

	mcpServer := mcpserver.NewMCPServer(
		"mcp-data-server",
		"1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithRecovery(),
	)
	handler.RegisterTools(mcpServer, toolHandler)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch strings.ToLower(cfg.Transport) {
	case "http":
		if err := startHTTP(ctx, cfg, mcpServer, authSvc, permSvc); err != nil && ctx.Err() == nil {
			log.Fatalf("http server: %v", err)
		}
	case "both":
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := startStdio(ctx, mcpServer); err != nil && ctx.Err() == nil {
				log.Printf("stdio server error: %v", err)
				cancel()
			}
		}()
		go func() {
			defer wg.Done()
			if err := startHTTP(ctx, cfg, mcpServer, authSvc, permSvc); err != nil && ctx.Err() == nil {
				log.Printf("http server error: %v", err)
				cancel()
			}
		}()
		<-ctx.Done()
		wg.Wait()
	default: // stdio
		if err := startStdio(ctx, mcpServer); err != nil && ctx.Err() == nil {
			log.Fatalf("stdio server: %v", err)
		}
	}
}

// startStdio 启动 stdio 传输，阻塞到 ctx 取消或发生错误。
func startStdio(ctx context.Context, mcpServer *mcpserver.MCPServer) error {
	log.Println("mcp-data-server started, listening on stdio")
	s := mcpserver.NewStdioServer(mcpServer)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Listen(ctx, os.Stdin, os.Stdout)
	}()

	select {
	case <-ctx.Done():
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("stdio stopped: %v", err)
			}
		case <-time.After(2 * time.Second):
		}
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
}

// startHTTP 启动 HTTP 传输（MCP over streamable-http + 权限后台 + 内嵌 Web），阻塞到 ctx 取消。
func startHTTP(ctx context.Context, cfg *config.Config, mcpServer *mcpserver.MCPServer, authSvc *service.AuthService, permSvc *service.PermissionService) error {
	addr := cfg.HTTPAddr
	if addr == "" {
		addr = ":8081"
	}

	httpSrv := mcpserver.NewStreamableHTTPServer(mcpServer, mcpserver.WithEndpointPath("/mcp"))
	// SSE 服务端：标准 MCP 旧版 SSE 传输（GET /sse 建立接收流 + POST /messages 发送请求），
	// 供 llama.cpp 等以 SSE 方式对接的 MCP 客户端连接（如 llama.cpp 的 --mcp-server sse://host:8081/sse）。
	// 复用同一个 mcpServer 实例，工具清单与鉴权逻辑完全一致。
	sseSrv := mcpserver.NewSSEServer(mcpServer,
		mcpserver.WithSSEEndpoint("/sse"),
		mcpserver.WithMessageEndpoint("/messages"),
		mcpserver.WithSSEDisableLocalhostProtection(true),
	)
	adminSrv := admin.New(authSvc, permSvc)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	r.Any("/mcp", gin.WrapH(httpSrv))
	r.Any("/sse", gin.WrapH(sseSrv))
	r.Any("/messages", gin.WrapH(sseSrv))
	r.Any("/api/admin/*path", gin.WrapH(adminSrv.Handler()))
	r.GET("/", gin.WrapH(web.StaticHandler(cfg.WebDir)))
	r.NoRoute(gin.WrapH(web.StaticHandler(cfg.WebDir)))

	srv := &http.Server{Addr: addr, Handler: r}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	log.Printf("mcp-data-server HTTP started: http://%s  (mcp=/mcp, sse=/sse, admin=/api/admin, web=/)", normalizeAddr(addr))
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown error: %v", err)
		}
		<-errCh
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}
