package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/handler"
	"company.com/mcp-data-server/internal/mask"
	"company.com/mcp-data-server/internal/mcp"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/service"
	"company.com/mcp-data-server/internal/transport"
)

func main() {
	cfg, err := config.Load("")
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

	toolHandler := handler.NewToolHandler(authSvc, querySvc, permSvc)
	server := mcp.NewServer("mcp-data-server", "1.0.0", handler.Tools, toolHandler.Handle)

	// 启动 stdio 传输层
	stdio := transport.NewStdio(server)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Println("mcp-data-server started, listening on stdio")
	if err := stdio.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("server stopped: %v", err)
	}
}
