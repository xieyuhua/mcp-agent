package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mark3labs/mcp-go/server"

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/admin"
	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/handler"
	"company.com/mcp-data-server/internal/mask"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/service"
	"company.com/mcp-data-server/internal/web"
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

	// 组装多层业务组件
	authSvc := service.NewAuthService(db, cfg.JWTSecret)
	auditSvc := service.NewAuditService(db)
	queryRepo := repository.NewQueryRepo(db)

	// 权限与脱敏配置 Resolver（从数据库读取，带缓存，可运行时可视化修改）
	permRepo := repository.NewPermissionRepo(db)
	authz := auth.NewResolver(permRepo)
	masker := mask.NewResolver(permRepo)
	// 启动时预热缓存（平台默认 + 各租户）
	if err := authz.Refresh(""); err != nil {
		log.Printf("warn: warm auth cache: %v", err)
	}
	if err := masker.Refresh(""); err != nil {
		log.Printf("warn: warm mask cache: %v", err)
	}

	querySvc := service.NewQueryService(queryRepo, auditSvc, authz, masker, cfg.MaskEnabled)
	permSvc := service.NewPermissionService(permRepo, authz, masker, auditSvc)
	webSvc := service.NewWebService()

	toolHandler := handler.NewToolHandler(authSvc, querySvc, permSvc, webSvc, cfg.WorkDir, cfg.SandboxEnabled)

	// 用 mcp-go 构建 MCP 服务，并注册全部业务工具。
	mcpServer := server.NewMCPServer(
		"mcp-data-server",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)
	handler.RegisterTools(mcpServer, toolHandler)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 根据 transport 配置启动对应传输层
	switch strings.ToLower(cfg.Transport) {
	case "http":
		startHTTP(ctx, cfg, mcpServer, authSvc, permSvc)
	case "both":
		go func() {
			log.Println("mcp-data-server stdio transport started")
			if err := server.ServeStdio(mcpServer); err != nil && ctx.Err() == nil {
				log.Printf("stdio stopped: %v", err)
			}
		}()
		startHTTP(ctx, cfg, mcpServer, authSvc, permSvc)
	default: // stdio（默认）
		log.Println("mcp-data-server started, listening on stdio")
		if err := server.ServeStdio(mcpServer); err != nil && ctx.Err() == nil {
			log.Fatalf("server stopped: %v", err)
		}
	}
}

// startHTTP 启动 HTTP 传输（MCP over streamable-http + 权限后台 + 内嵌 Web）。
func startHTTP(ctx context.Context, cfg *config.Config, mcpServer *server.MCPServer, authSvc *service.AuthService, permSvc *service.PermissionService) {
	addr := cfg.HTTPAddr
	if addr == "" {
		addr = ":8081"
	}
	// mcp-go 的 streamable-http 处理器：实现 http.Handler，自动处理 GET(SSE)/POST，并管理会话。
	httpSrv := server.NewStreamableHTTPServer(mcpServer, server.WithEndpointPath("/mcp"))
	adminSrv := admin.New(authSvc, permSvc)

	mux := http.NewServeMux()
	mux.Handle("/mcp", httpSrv)                  // streamable-http（GET 建流 + POST 收发）
	mux.Handle("/api/admin/", adminSrv.Handler()) // 权限后台 REST API
	mux.Handle("/", web.StaticHandler(cfg.WebDir)) // 内嵌/外部 Web 页面

	handler := withCORS(mux)
	log.Printf("mcp-data-server HTTP started: http://%s  (mcp=/mcp, admin=/api/admin, web=/)", normalizeAddr(addr))
	if err := http.ListenAndServe(addr, handler); err != nil && ctx.Err() == nil {
		log.Fatalf("http server stopped: %v", err)
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}
