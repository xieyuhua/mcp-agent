package mcpclient

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestHandshake 验证 Agent 能拉起 mcp-data-server 子进程并完成握手、登录、工具调用。
func TestHandshake(t *testing.T) {
	serverPath, err := filepath.Abs("../../mcp-data-server/main.exe")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Start(StartConfig{
		ServerPath:  serverPath,
		DBDialect:   "sqlite",
		DBDsn:       "./data.db",
		MaskEnabled: true,
		SeedDemo:    true,
	})
	if err != nil {
		t.Fatalf("start mcp: %v", err)
	}
	defer c.Close()

	if !c.HasTool("run_sql") || !c.HasTool("query_table") {
		t.Fatalf("expected tools missing: %v", c.Tools())
	}

	// 登录
	loginText, isErr, err := c.CallTool("auth_login", map[string]interface{}{
		"username": "admin",
		"password": "admin123",
	})
	if err != nil {
		t.Fatalf("auth_login: %v", err)
	}
	if isErr {
		t.Fatalf("auth_login error: %s", loginText)
	}
	var login struct {
		Token string `json:"token"`
		Role  string `json:"role"`
	}
	if err := json.Unmarshal([]byte(loginText), &login); err != nil {
		t.Fatalf("parse login: %v", err)
	}
	if login.Token == "" {
		t.Fatal("empty token")
	}
	t.Logf("login ok, role=%s", login.Role)

	// 用 token 调用 describe_table
	descText, isErr, err := c.CallTool("describe_table", map[string]interface{}{
		"token": login.Token,
		"table": "orders",
	})
	if err != nil {
		t.Fatalf("describe_table: %v", err)
	}
	if isErr {
		t.Fatalf("describe_table error: %s", descText)
	}
	if descText == "" {
		t.Fatal("empty describe_table result")
	}
	t.Logf("describe_table orders ok: %s", descText)

	// 用 token 执行一条只读 SQL
	sqlText, isErr, err := c.CallTool("run_sql", map[string]interface{}{
		"token": login.Token,
		"sql":   "SELECT status, COUNT(*) AS cnt, SUM(amount) AS total FROM orders GROUP BY status",
	})
	if err != nil {
		t.Fatalf("run_sql: %v", err)
	}
	if isErr {
		t.Fatalf("run_sql error: %s", sqlText)
	}
	t.Logf("run_sql ok: %s", sqlText)
}
