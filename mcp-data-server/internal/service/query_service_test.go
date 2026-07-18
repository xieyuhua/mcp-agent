package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/mask"
	"company.com/mcp-data-server/internal/repository"
)

func newTestDB(t *testing.T) (*repository.QueryRepo, *repository.PermissionRepo, *repository.RoleRepo, *auth.Resolver, *mask.Resolver) {
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
	permRepo := repository.NewPermissionRepo(db)
	roleRepo := repository.NewRoleRepo(db)
	authz := auth.NewResolver(permRepo)
	masker := mask.NewResolver(permRepo)
	if err := authz.Refresh(""); err != nil {
		t.Fatalf("authz refresh: %v", err)
	}
	if err := masker.Refresh(""); err != nil {
		t.Fatalf("masker refresh: %v", err)
	}
	return repository.NewQueryRepo(db), permRepo, roleRepo, authz, masker
}

func testPermSvc(permRepo *repository.PermissionRepo, roleRepo *repository.RoleRepo, authz *auth.Resolver, masker *mask.Resolver) *PermissionService {
	return NewPermissionService(permRepo, roleRepo, authz, masker, NewAuditService(permRepo.DB()))
}

func TestStoreManagerIsolationAndMasking(t *testing.T) {
	repo, _, _, authz, masker := newTestDB(t)
	authSvc := NewAuthService(repo.DB(), "test-secret")
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()), authz, masker, true)

	_, tc, err := authSvc.Login("store1", "store123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if tc.Role != "store_manager" || tc.StoreID != "s1" {
		t.Fatalf("unexpected context: %+v", tc)
	}

	rows, err := querySvc.QueryTable(context.Background(), tc, repository.QueryRequest{
		Table: "customers", Limit: 100,
	}, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("store_manager should see only 2 customers in s1, got %d", len(rows))
	}
	for _, r := range rows {
		if r["store_id"] != "s1" {
			t.Fatalf("data isolation broken, leaked row store_id=%v", r["store_id"])
		}
		phone, _ := r["phone"].(string)
		if !strings.Contains(phone, "****") {
			t.Fatalf("phone not masked: %v", phone)
		}
		idcard, _ := r["id_card"].(string)
		if !strings.Contains(idcard, "****") {
			t.Fatalf("id_card not masked: %v", idcard)
		}
	}
}

func TestSuperAdminCrossTenantSQL(t *testing.T) {
	repo, _, _, authz, masker := newTestDB(t)
	authSvc := NewAuthService(repo.DB(), "test-secret")
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()), authz, masker, true)

	_, tc, err := authSvc.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	rows, err := querySvc.RunSQL(context.Background(), tc,
		"select tenant_id, count(*) as cnt from customers group by tenant_id", nil)
	if err != nil {
		t.Fatalf("run_sql: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("super_admin should see 2 tenants, got %d", len(rows))
	}
}

func TestStoreManagerCannotRunRawSQL(t *testing.T) {
	repo, _, _, authz, masker := newTestDB(t)
	authSvc := NewAuthService(repo.DB(), "test-secret")
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()), authz, masker, true)

	_, tc, err := authSvc.Login("store1", "store123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_, err = querySvc.RunSQL(context.Background(), tc, "select 1", nil)
	if err == nil {
		t.Fatalf("store_manager should be denied raw SQL")
	}
}

func TestTenantIsolationBetweenTenants(t *testing.T) {
	repo, _, _, authz, masker := newTestDB(t)
	authSvc := NewAuthService(repo.DB(), "test-secret")
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()), authz, masker, true)

	_, tc, _ := authSvc.Login("store1", "store123")
	rows, err := querySvc.QueryTable(context.Background(), tc, repository.QueryRequest{
		Table: "customers", Filters: map[string]interface{}{"tenant_id": "t2"}, Limit: 100,
	}, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("tenant isolation broken: store_manager of t1 saw t2 rows: %d", len(rows))
	}
}

// TestPermSetTakesEffect 验证权限可视化设置后即时生效：
// 将 store_manager 的数据范围从 store 改为 tenant，店长查询立即能看到整个租户。
func TestPermSetTakesEffect(t *testing.T) {
	repo, permRepo, roleRepo, authz, masker := newTestDB(t)
	authSvc := NewAuthService(repo.DB(), "test-secret")
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()), authz, masker, true)
	permSvc := testPermSvc(permRepo, roleRepo, authz, masker)

	_, adminTc, err := authSvc.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("login admin: %v", err)
	}
	_, storeTc, err := authSvc.Login("store1", "store123")
	if err != nil {
		t.Fatalf("login store: %v", err)
	}

	// 修改前：店长仅见本门店 2 条
	before, _ := querySvc.QueryTable(context.Background(), storeTc, repository.QueryRequest{Table: "customers", Limit: 100}, nil)
	if len(before) != 2 {
		t.Fatalf("precondition failed, expect 2 rows, got %d", len(before))
	}

	// 可视化设置：store_manager 在 t1 租户的数据范围改为 tenant
	_, err = permSvc.SetPolicy(adminTc, SetPolicyRequest{
		TenantID:      "t1",
		Role:          "store_manager",
		DataScope:     "tenant",
		AllowedTables: []string{"customers", "orders"},
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	// 修改后：店长应能看到整个 t1 租户（3 条）
	after, err := querySvc.QueryTable(context.Background(), storeTc, repository.QueryRequest{Table: "customers", Limit: 100}, nil)
	if err != nil {
		t.Fatalf("query after: %v", err)
	}
	if len(after) != 3 {
		t.Fatalf("perm_set should widen scope to tenant (3 rows), got %d", len(after))
	}
	for _, r := range after {
		if r["tenant_id"] != "t1" {
			t.Fatalf("scope leak across tenant: %v", r["tenant_id"])
		}
	}
}

// TestMaskSetTakesEffect 验证脱敏规则可视化关闭后即时生效。
func TestMaskSetTakesEffect(t *testing.T) {
	repo, permRepo, roleRepo, authz, masker := newTestDB(t)
	authSvc := NewAuthService(repo.DB(), "test-secret")
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()), authz, masker, true)
	permSvc := testPermSvc(permRepo, roleRepo, authz, masker)

	_, adminTc, _ := authSvc.Login("admin", "admin123")
	_, storeTc, _ := authSvc.Login("store1", "store123")

	// 关闭 t1 的 customers.phone 脱敏
	_, err := permSvc.SetMaskRule(adminTc, SetMaskRuleRequest{
		TenantID: "t1", Table: "customers", Column: "phone", MaskType: "phone", Enabled: false,
	})
	if err != nil {
		t.Fatalf("set mask rule: %v", err)
	}
	rows, err := querySvc.QueryTable(context.Background(), storeTc, repository.QueryRequest{Table: "customers", Limit: 100}, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, r := range rows {
		phone, _ := r["phone"].(string)
		if strings.Contains(phone, "****") {
			t.Fatalf("mask should be disabled but got masked: %v", phone)
		}
	}
}

// TestNonAdminCannotManagePerm 验证非平台运营无法修改权限配置。
func TestNonAdminCannotManagePerm(t *testing.T) {
	repo, permRepo, roleRepo, authz, masker := newTestDB(t)
	authSvc := NewAuthService(repo.DB(), "test-secret")
	permSvc := testPermSvc(permRepo, roleRepo, authz, masker)

	_, storeTc, _ := authSvc.Login("store1", "store123")
	_, err := permSvc.SetPolicy(storeTc, SetPolicyRequest{
		TenantID: "t1", Role: "store_manager", DataScope: "all",
	})
	if err == nil {
		t.Fatalf("non-admin should be denied from managing permission")
	}
}

// TestFieldPermissionTakesEffect 验证字段权限对 describe_table、query_table、run_sql 均生效。
func TestFieldPermissionTakesEffect(t *testing.T) {
	repo, permRepo, roleRepo, authz, masker := newTestDB(t)
	authSvc := NewAuthService(repo.DB(), "test-secret")
	querySvc := NewQueryService(repo, NewAuditService(repo.DB()), authz, masker, true)
	permSvc := testPermSvc(permRepo, roleRepo, authz, masker)

	_, adminTc, _ := authSvc.Login("admin", "admin123")
	_, storeTc, _ := authSvc.Login("store1", "store123")

	// 对 store_manager 隐藏 customers.id_card 与 customers.phone
	for _, col := range []string{"id_card", "phone"} {
		_, err := permSvc.SetFieldPermission(adminTc, SetFieldPermissionRequest{
			TenantID: "t1", Role: "store_manager", Table: "customers", Column: col, Hidden: true,
		})
		if err != nil {
			t.Fatalf("set field permission %s: %v", col, err)
		}
	}

	// describe_table 不应返回隐藏字段
	cols, err := querySvc.DescribeTable(storeTc, "customers")
	if err != nil {
		t.Fatalf("describe_table: %v", err)
	}
	for _, c := range cols {
		if c == "id_card" || c == "phone" {
			t.Fatalf("describe_table should not expose hidden column %s", c)
		}
	}

	// query_table 返回结果应剔除隐藏字段
	rows, err := querySvc.QueryTable(context.Background(), storeTc, repository.QueryRequest{Table: "customers", Limit: 100}, nil)
	if err != nil {
		t.Fatalf("query_table: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expect rows")
	}
	for _, r := range rows {
		if _, ok := r["id_card"]; ok {
			t.Fatalf("id_card should be hidden in query result")
		}
		if _, ok := r["phone"]; ok {
			t.Fatalf("phone should be hidden in query result")
		}
	}

	// 显式请求隐藏字段应被拒绝
	if _, err := querySvc.QueryTable(context.Background(), storeTc, repository.QueryRequest{
		Table: "customers", Fields: []string{"id"}, Limit: 100,
	}, nil); err != nil {
		t.Fatalf("query visible field should work: %v", err)
	}
	if _, err := querySvc.QueryTable(context.Background(), storeTc, repository.QueryRequest{
		Table: "customers", Fields: []string{"id_card"}, Limit: 100,
	}, nil); err == nil {
		t.Fatalf("query hidden field should be denied")
	}

	// run_sql 返回结果也应剔除隐藏字段（super_admin 设置自己角色隐藏也生效）
	_, err = permSvc.SetFieldPermission(adminTc, SetFieldPermissionRequest{
		TenantID: "t1", Role: "super_admin", Table: "customers", Column: "phone", Hidden: true,
	})
	if err != nil {
		t.Fatalf("set super_admin field permission: %v", err)
	}
	// 重新登录以刷新上下文（tenant_id 与 role 不变）
	_, adminTc, _ = authSvc.Login("admin", "admin123")
	sqlRows, err := querySvc.RunSQL(context.Background(), adminTc, "select * from customers limit 1", nil)
	if err != nil {
		t.Fatalf("run_sql: %v", err)
	}
	for _, r := range sqlRows {
		if _, ok := r["phone"]; ok {
			t.Fatalf("run_sql should filter hidden phone")
		}
	}
}
