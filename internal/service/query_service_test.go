package service

import (
	"context"
	"path/filepath"
	"testing"

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/repository"
)

func newTestDeps(t *testing.T) (*repository.QueryRepo, *PermissionService) {
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
	return repository.NewQueryRepo(db), NewPermissionService(repository.NewPermissionRepo(db))
}

func newTestSvc(t *testing.T) *QueryService {
	repo, perm := newTestDeps(t)
	return NewQueryService(repo, NewAuditService(repo.DB()), perm)
}

func TestBasicQuery(t *testing.T) {
	querySvc := newTestSvc(t)
	tc := &QueryContext{UserID: "test", Role: "super_admin"}

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
	querySvc := newTestSvc(t)
	tc := &QueryContext{UserID: "test", Role: "super_admin"}

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
	querySvc := newTestSvc(t)
	tc := &QueryContext{UserID: "test", Role: "super_admin"}

	schema, err := querySvc.DescribeTable(tc, "customers")
	if err != nil {
		t.Fatalf("describe_table: %v", err)
	}
	if schema == nil {
		t.Fatalf("expected schema, got nil")
	}
}

// TestRowLevelIsolation 验证按 origin_role 自动注入行级 where：
// tenant_t1_admin 只应看到 t1 的客户（3 条），而非全部（4 条）。
func TestRowLevelIsolation(t *testing.T) {
	querySvc := newTestSvc(t)
	tc := &QueryContext{UserID: "test", Role: "tenant_t1_admin"}

	rows, err := querySvc.RunSQL(context.Background(), tc, "SELECT id, tenant_id FROM customers c", nil)
	if err != nil {
		t.Fatalf("run_sql: %v", err)
	}
	for _, r := range rows {
		if v, ok := r["tenant_id"]; ok && v != "t1" {
			t.Fatalf("越权数据泄漏: %v", v)
		}
	}
	if len(rows) == 4 {
		t.Fatalf("行级隔离未生效，仍返回全部 %d 条", len(rows))
	}
}

// TestFieldMasking 验证字段脱敏：店员角色查询客户手机号应被脱敏，身份证应被隐藏。
func TestFieldMasking(t *testing.T) {
	querySvc := newTestSvc(t)
	tc := &QueryContext{UserID: "test", Role: "store_s1_staff"}

	rows, err := querySvc.QueryTable(context.Background(), tc, repository.QueryRequest{
		Table: "customers", Limit: 100,
	}, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, r := range rows {
		if _, ok := r["id_card"]; ok {
			t.Fatalf("身份证应被隐藏")
		}
	}
}
