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

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/admin"
	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/handler"
	"company.com/mcp-data-server/internal/mask"
	"company.com/mcp-data-server/internal/mcp"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/service"
	"company.com/mcp-data-server/internal/transport"
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

	toolHandler := handler.NewToolHandler(authSvc, querySvc, permSvc, cfg.WorkDir)
	server := mcp.NewServer("mcp-data-server", "1.0.0", handler.Tools, toolHandler.Handle)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 根据 transport 配置启动对应传输层
	switch strings.ToLower(cfg.Transport) {
	case "http":
		startHTTP(ctx, cfg, server, authSvc, permSvc)
	case "both":
		stdio := transport.NewStdio(server)
		go func() {
			log.Println("mcp-data-server stdio transport started")
			if err := stdio.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("stdio stopped: %v", err)
			}
		}()
		startHTTP(ctx, cfg, server, authSvc, permSvc)
	default: // stdio（默认）
		stdio := transport.NewStdio(server)
		log.Println("mcp-data-server started, listening on stdio")
		if err := stdio.Run(ctx); err != nil && ctx.Err() == nil {
			log.Fatalf("server stopped: %v", err)
		}
	}
}

// startHTTP 启动 HTTP 传输（MCP over HTTP + 权限后台 + 内嵌 Web）。
func startHTTP(ctx context.Context, cfg *config.Config, server *mcp.Server, authSvc *service.AuthService, permSvc *service.PermissionService) {
	addr := cfg.HTTPAddr
	if addr == "" {
		addr = ":8081"
	}
	httpSrv := transport.NewHTTPServer(server)
	adminSrv := admin.New(authSvc, permSvc)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", httpSrv.HandleStreamable)    // streamable-http
	mux.HandleFunc("/sse", httpSrv.HandleSSE)           // 旧版 sse 接收流
	mux.HandleFunc("/messages", httpSrv.HandleMessages) // 旧版 sse 消息端点
	mux.Handle("/api/admin/", adminSrv.Handler())       // 权限后台 REST API
	mux.Handle("/", web.StaticHandler(cfg.WebDir))      // 内嵌/外部 Web 页面

	handler := withCORS(mux)
	log.Printf("mcp-data-server HTTP started: http://%s  (mcp=%s, sse=%s, admin=/api/admin, web=/)", normalizeAddr(addr), "/mcp", "/sse")
	if err := http.ListenAndServe(addr, handler); err != nil && ctx.Err() == nil {
		log.Fatalf("http server stopped: %v", err)
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
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
