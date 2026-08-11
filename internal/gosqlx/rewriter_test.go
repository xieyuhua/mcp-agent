package gosqlx

import (
	"strings"
	"testing"
)

func TestInjectWhere_SimpleWithAlias(t *testing.T) {
	rules := []TableRule{
		{Table: "customers", Condition: "{alias}.tenant_id = 't1'"},
	}
	sql := "SELECT c.name FROM customers c WHERE c.status = 'active'"
	out, err := InjectWhere(sql, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "c.tenant_id = 't1'") {
		t.Fatalf("别名未适配: %s", out)
	}
	if !strings.Contains(out, "c.status") {
		t.Fatalf("原条件丢失: %s", out)
	}
}

func TestInjectWhere_NoAlias(t *testing.T) {
	rules := []TableRule{{Table: "orders", Condition: "{alias}.tenant_id = 't1'"}}
	sql := "SELECT * FROM orders"
	out, err := InjectWhere(sql, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "orders.tenant_id = 't1'") {
		t.Fatalf("无别名应使用表名: %s", out)
	}
	if !strings.Contains(strings.ToUpper(out), "WHERE") {
		t.Fatalf("应新增 WHERE: %s", out)
	}
}

func TestInjectWhere_MultiTableJoin(t *testing.T) {
	rules := []TableRule{
		{Table: "customers", Condition: "{alias}.tenant_id = 't1'"},
		{Table: "orders", Condition: "{alias}.tenant_id = 't1'"},
	}
	sql := "SELECT c.name, o.amount FROM customers c JOIN orders o ON c.id = o.customer_id WHERE o.status = 'paid'"
	out, err := InjectWhere(sql, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "c.tenant_id = 't1'") || !strings.Contains(out, "o.tenant_id = 't1'") {
		t.Fatalf("多表别名注入失败: %s", out)
	}
}

func TestInjectWhere_Subquery(t *testing.T) {
	rules := []TableRule{{Table: "orders", Condition: "{alias}.tenant_id = 't1'"}}
	sql := "SELECT * FROM (SELECT o.customer_id FROM orders o) t"
	out, err := InjectWhere(sql, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "o.tenant_id = 't1'") {
		t.Fatalf("子查询未注入: %s", out)
	}
}

func TestValidate_RejectDanger(t *testing.T) {
	if _, err := Validate("SELECT * FROM customers; DROP TABLE customers", ValidateOptions{}); err == nil {
		t.Fatal("应拒绝多语句")
	}
	if _, err := Validate("DELETE FROM customers", ValidateOptions{}); err == nil {
		t.Fatal("应拒绝 DELETE")
	}
	if _, err := Validate("SELECT * FROM customers -- x", ValidateOptions{}); err == nil {
		t.Fatal("应拒绝注释")
	}
}

func TestValidate_TableWhitelist(t *testing.T) {
	_, err := Validate("SELECT * FROM secret_table", ValidateOptions{AllowedTables: []string{"customers", "orders"}})
	if err == nil {
		t.Fatal("应拒绝白名单外的表")
	}
	if _, err := Validate("SELECT created_at FROM customers", ValidateOptions{AllowedTables: []string{"customers"}}); err != nil {
		t.Fatalf("created_at 不应被误判为 create: %v", err)
	}
}
