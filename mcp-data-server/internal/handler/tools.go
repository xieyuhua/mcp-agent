package handler

import "company.com/mcp-data-server/internal/mcp"

// Tools 对外暴露的 MCP 工具清单。
var Tools = []mcp.Tool{
	{
		Name:        "auth_login",
		Description: "使用用户名/密码登录，获取访问令牌（token）。后续工具调用均需携带该 token。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"username": map[string]interface{}{"type": "string", "description": "用户名"},
				"password": map[string]interface{}{"type": "string", "description": "密码"},
			},
			"required": []string{"username", "password"},
		},
	},
	{
		Name:        "query_table",
		Description: "结构化安全查询数据表。自动叠加租户/区域/门店隔离，并对敏感字段脱敏。支持 customers / orders。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":   map[string]interface{}{"type": "string", "description": "登录令牌"},
				"table":   map[string]interface{}{"type": "string", "description": "表名: customers | orders"},
				"fields":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "返回字段，留空返回全部"},
				"filters": map[string]interface{}{"type": "object", "description": "等值过滤条件，如 {\"status\":\"paid\"}"},
				"order":   map[string]interface{}{"type": "string", "description": "排序，如 created_at desc"},
				"limit":   map[string]interface{}{"type": "integer", "description": "返回行数上限，默认100，最大1000"},
				"offset":  map[string]interface{}{"type": "integer", "description": "偏移"},
			},
			"required": []string{"token", "table"},
		},
	},
	{
		Name:        "run_sql",
		Description:  "执行原生只读 SQL（仅平台运营 super_admin 可用，自动拦截危险关键字防注入）。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
				"sql":   map[string]interface{}{"type": "string", "description": "SELECT 语句"},
			},
			"required": []string{"token", "sql"},
		},
	},
	{
		Name:        "describe_table",
		Description: "查看数据表的字段结构（供数据分析师了解 schema）。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
				"table": map[string]interface{}{"type": "string", "description": "表名"},
			},
			"required": []string{"token", "table"},
		},
	},
	// --- 权限可视化设置管理（仅 super_admin） ---
	{
		Name:        "perm_view",
		Description: "查看角色权限策略（数据范围/表白名单/原SQL权限）。可视化权限管理的只读视图，仅平台运营可用。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":    map[string]interface{}{"type": "string", "description": "登录令牌"},
				"tenant_id": map[string]interface{}{"type": "string", "description": "租户ID，留空表示当前租户"},
			},
			"required": []string{"token"},
		},
	},
	{
		Name:        "perm_set",
		Description: "设置/修改角色权限策略：数据范围(all/tenant/region/store)、可访问表白名单、是否允许原SQL。修改后即时生效，仅平台运营可用。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":         map[string]interface{}{"type": "string", "description": "登录令牌"},
				"tenant_id":     map[string]interface{}{"type": "string", "description": "租户ID，留空=当前租户"},
				"role":          map[string]interface{}{"type": "string", "description": "角色: super_admin|region_manager|store_manager|staff|analyst"},
				"data_scope":    map[string]interface{}{"type": "string", "description": "数据范围: all|tenant|region|store"},
				"allowed_tables": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "可访问表白名单"},
				"can_raw_sql":   map[string]interface{}{"type": "boolean", "description": "是否允许执行原生SQL"},
			},
			"required": []string{"token", "role", "data_scope"},
		},
	},
	{
		Name:        "perm_delete",
		Description: "删除某租户级角色策略（回退到平台默认）。仅平台运营可用。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":     map[string]interface{}{"type": "string", "description": "登录令牌"},
				"tenant_id": map[string]interface{}{"type": "string", "description": "租户ID"},
				"role":      map[string]interface{}{"type": "string", "description": "角色"},
			},
			"required": []string{"token", "tenant_id", "role"},
		},
	},
	{
		Name:        "mask_view",
		Description: "查看数据脱敏规则（表.列 -> 脱敏类型）。可视化脱敏管理的只读视图，仅平台运营可用。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":     map[string]interface{}{"type": "string", "description": "登录令牌"},
				"tenant_id": map[string]interface{}{"type": "string", "description": "租户ID，留空=当前租户"},
			},
			"required": []string{"token"},
		},
	},
	{
		Name:        "mask_set",
		Description: "设置/修改列级脱敏规则（phone/email/idcard/name/money/secret）。修改后即时生效，仅平台运营可用。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":     map[string]interface{}{"type": "string", "description": "登录令牌"},
				"tenant_id": map[string]interface{}{"type": "string", "description": "租户ID，留空=当前租户"},
				"table":     map[string]interface{}{"type": "string", "description": "表名"},
				"column":    map[string]interface{}{"type": "string", "description": "列名"},
				"mask_type": map[string]interface{}{"type": "string", "description": "脱敏类型: phone|email|idcard|name|money|secret"},
				"enabled":   map[string]interface{}{"type": "boolean", "description": "是否启用，默认true"},
			},
			"required": []string{"token", "table", "column", "mask_type"},
		},
	},
	{
		Name:        "mask_delete",
		Description: "删除某租户级脱敏规则。仅平台运营可用。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":     map[string]interface{}{"type": "string", "description": "登录令牌"},
				"tenant_id": map[string]interface{}{"type": "string", "description": "租户ID"},
				"table":     map[string]interface{}{"type": "string", "description": "表名"},
				"column":    map[string]interface{}{"type": "string", "description": "列名"},
			},
			"required": []string{"token", "tenant_id", "table", "column"},
		},
	},

	// --- 文件 / 目录读写工具（沙箱在 work_dir 内） ---
	{
		Name:        "read_file",
		Description: "读取文本文件内容。路径相对于工作目录（沙箱）。用于查看配置文件、日志、数据导出等。二进制文件可能乱码。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":    map[string]interface{}{"type": "string", "description": "登录令牌"},
				"path":     map[string]interface{}{"type": "string", "description": "文件路径，相对于工作目录，如 reports/summary.txt"},
				"max_bytes": map[string]interface{}{"type": "integer", "description": "最多读取字节数，默认 65536，最大 1048576"},
			},
			"required": []string{"token", "path"},
		},
	},
	{
		Name:        "write_file",
		Description: "写入文本文件（覆盖）。父目录不存在时自动创建。路径相对于工作目录。用于生成报告、导出分析结果。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":    map[string]interface{}{"type": "string", "description": "登录令牌"},
				"path":     map[string]interface{}{"type": "string", "description": "文件路径，相对于工作目录，如 reports/summary.txt"},
				"content":  map[string]interface{}{"type": "string", "description": "要写入的文本内容"},
			},
			"required": []string{"token", "path", "content"},
		},
	},
	{
		Name:        "append_file",
		Description: "向文本文件末尾追加内容（文件不存在则创建）。路径相对于工作目录。用于日志追加、结果累积。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token":   map[string]interface{}{"type": "string", "description": "登录令牌"},
				"path":    map[string]interface{}{"type": "string", "description": "文件路径，相对于工作目录"},
				"content": map[string]interface{}{"type": "string", "description": "要追加的文本内容"},
			},
			"required": []string{"token", "path", "content"},
		},
	},
	{
		Name:        "list_dir",
		Description: "列出目录下的文件和子目录（含名称、类型、大小、修改时间）。路径相对于工作目录，留空表示根目录。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
				"path":  map[string]interface{}{"type": "string", "description": "目录路径，相对于工作目录，留空=根目录"},
			},
			"required": []string{"token"},
		},
	},
	{
		Name:        "make_dir",
		Description: "创建目录（含多级父目录）。路径相对于工作目录。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
				"path":  map[string]interface{}{"type": "string", "description": "目录路径，相对于工作目录"},
			},
			"required": []string{"token", "path"},
		},
	},
	{
		Name:        "delete_file",
		Description: "删除一个文件（不会删除目录）。路径相对于工作目录。删除前请确认路径正确。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
				"path":  map[string]interface{}{"type": "string", "description": "文件路径，相对于工作目录"},
			},
			"required": []string{"token", "path"},
		},
	},
	{
		Name:        "read_dir_tree",
		Description: "递归列出目录树（最多两层，避免结果过大）。返回每个条目的相对路径与类型。用于了解工作目录整体结构。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
				"path":  map[string]interface{}{"type": "string", "description": "起始目录，相对于工作目录，留空=根目录"},
			},
			"required": []string{"token"},
		},
	},
}
