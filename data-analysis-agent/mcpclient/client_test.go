package mcpclient

import (
	"path/filepath"
	"testing"
)

// TestHandshake 验证 Agent 能拉起 mcp-data-server 子进程并完成握手、工具调用。
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

	descText, isErr, err := c.CallTool("describe_table", map[string]interface{}{
		"table": "orders",
	}, nil)
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

	sqlText, isErr, err := c.CallTool("run_sql", map[string]interface{}{
		"sql": "SELECT status, COUNT(*) AS cnt, SUM(amount) AS total FROM orders GROUP BY status",
	}, nil)
	if err != nil {
		t.Fatalf("run_sql: %v", err)
	}
	if isErr {
		t.Fatalf("run_sql error: %s", sqlText)
	}
	t.Logf("run_sql ok: %s", sqlText)
}
