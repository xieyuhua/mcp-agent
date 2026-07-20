package service

import (
	"context"
	"path/filepath"
	"testing"

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/repository"
)

func newTestDB(t *testing.T) *repository.QueryRepo {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := repository.OpenDB(&config.Config{DBDialect: "sqlite", DBDSN: dsn})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := repository.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := repository.Seed(db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	return repository.NewQueryRepo(db)
}

func TestBasicQuery(t *testing.T) {
	repo := newTestDB(t)
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()))
	tc := &QueryContext{TenantID: "", UserID: "test", Role: "super_admin"}

	rows, err := querySvc.QueryTable(context.Background(), tc, repository.QueryRequest{
		Table: "customers", Limit: 100,
	}, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 customers, got %d", len(rows))
	}
}

func TestRunSQL(t *testing.T) {
	repo := newTestDB(t)
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()))
	tc := &QueryContext{TenantID: "", UserID: "test", Role: "super_admin"}

	rows, err := querySvc.RunSQL(context.Background(), tc,
		"select tenant_id, count(*) as cnt from customers group by tenant_id", nil)
	if err != nil {
		t.Fatalf("run_sql: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 tenants, got %d", len(rows))
	}
}

func TestDescribeTable(t *testing.T) {
	repo := newTestDB(t)
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()))
	tc := &QueryContext{TenantID: "", UserID: "test", Role: "super_admin"}

	cols, err := querySvc.DescribeTable(tc, "customers")
	if err != nil {
		t.Fatalf("describe_table: %v", err)
	}
	if len(cols) == 0 {
		t.Fatalf("expected columns, got none")
	}
}
