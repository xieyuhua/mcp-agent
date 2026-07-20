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
	"company.com/mcp-data-server/internal/handler"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/service"

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

	auditSvc := service.NewAuditService(db)
	queryRepo := repository.NewQueryRepo(db)

	querySvc := service.NewQueryService(queryRepo, auditSvc)
	webSvc := service.NewWebService(cfg.SearchProvider)
	weatherSvc := service.NewWeatherService()

	toolHandler := handler.NewToolHandler(querySvc, webSvc, weatherSvc, cfg.WorkDir, cfg.SandboxEnabled)

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
		if err := startHTTP(ctx, cfg, mcpServer, toolHandler); err != nil && ctx.Err() == nil {
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
			if err := startHTTP(ctx, cfg, mcpServer, toolHandler); err != nil && ctx.Err() == nil {
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

// startHTTP 启动 HTTP 传输（MCP over streamable-http），阻塞到 ctx 取消。
func startHTTP(ctx context.Context, cfg *config.Config, mcpServer *mcpserver.MCPServer, toolHandler *handler.ToolHandler) error {
	addr := cfg.HTTPAddr
	if addr == "" {
		addr = ":8081"
	}

	httpSrv := mcpserver.NewStreamableHTTPServer(mcpServer,
		mcpserver.WithEndpointPath("/mcp"),
		mcpserver.WithStateLess(true),
	)
	sseSrv := mcpserver.NewSSEServer(mcpServer,
		mcpserver.WithSSEEndpoint("/sse"),
		mcpserver.WithMessageEndpoint("/messages"),
		mcpserver.WithSSEDisableLocalhostProtection(true),
	)
	llamaHandler := handler.NewLlamaToolHandler(toolHandler)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	r.Any("/mcp", gin.WrapH(httpSrv))
	r.Any("/sse", gin.WrapH(sseSrv))
	r.Any("/messages", gin.WrapH(sseSrv))
	r.GET("/api/llama/tools", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"tools": llamaHandler.ListTools()})
	})
	r.POST("/api/llama/tools/call", func(c *gin.Context) {
		var req handler.LlamaToolCallRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		res, err := llamaHandler.CallTool(c.Request.Context(), req)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": res})
	})

	srv := &http.Server{Addr: addr, Handler: r}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	log.Printf("mcp-data-server HTTP started: http://%s  (mcp=/mcp, sse=/sse)", normalizeAddr(addr))
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
	// llama.cpp 网页客户端会在 preflight 时把 access-control-allow-origin 等当作请求头发出，
	// 必须全部放行，否则浏览器报 "not allowed by Access-Control-Allow-Headers"。
	c.Writer.Header().Set("Access-Control-Allow-Headers",
		"Content-Type, Authorization, Mcp-Session-Id, mcp-protocol-version, Accept, access-control-allow-origin, Origin, X-Requested-With")
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
