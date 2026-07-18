package handler

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTools 把全部业务工具注册到 mcp-go 的 MCP 服务实例上。
// 业务实现仍由 ToolHandler 的各方法提供（返回 (interface{}, error)），
// 这里只负责把每个工具声明为 mcp.Tool 并绑定 handler，最后把结果序列化回 MCP 协议。
func RegisterTools(s *server.MCPServer, h *ToolHandler) {
	// --- 登录 ---
	s.AddTool(
		mcp.NewTool("auth_login",
			mcp.WithDescription("使用用户名/密码登录，获取访问令牌（token）。后续工具调用均需携带该 token。"),
			mcp.WithString("username", mcp.Required(), mcp.Description("用户名")),
			mcp.WithString("password", mcp.Required(), mcp.Description("密码")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.login(req.GetArguments())), nil
		},
	)

	// --- 数据查询 ---
	s.AddTool(
		mcp.NewTool("query_table",
			mcp.WithDescription("结构化安全查询数据表。自动叠加租户/区域/门店隔离，并对敏感字段脱敏。支持 customers / orders。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("table", mcp.Required(), mcp.Description("表名: customers | orders")),
			mcp.WithArray("fields", mcp.Description("返回字段，留空返回全部")),
			mcp.WithObject("filters", mcp.Description("等值过滤条件，如 {\"status\":\"paid\"}")),
			mcp.WithString("order", mcp.Description("排序，如 created_at desc")),
			mcp.WithNumber("limit", mcp.Description("返回行数上限，默认100，最大1000")),
			mcp.WithNumber("offset", mcp.Description("偏移")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.queryTable(ctx, req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("run_sql",
			mcp.WithDescription("执行原生只读 SQL（仅平台运营 super_admin 可用，自动拦截危险关键字防注入）。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("sql", mcp.Required(), mcp.Description("SELECT 语句")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.runSQL(ctx, req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("describe_table",
			mcp.WithDescription("查看数据表的字段结构（供数据分析师了解 schema）。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("table", mcp.Required(), mcp.Description("表名")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.describeTable(ctx, req.GetArguments())), nil
		},
	)

	// --- 权限可视化设置管理（仅 super_admin） ---
	s.AddTool(
		mcp.NewTool("perm_view",
			mcp.WithDescription("查看角色权限策略（数据范围/表白名单/原SQL权限）。可视化权限管理的只读视图，仅平台运营可用。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("tenant_id", mcp.Description("租户ID，留空表示当前租户")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.permView(ctx, req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("perm_set",
			mcp.WithDescription("设置/修改角色权限策略：数据范围(all/tenant/region/store)、可访问表白名单、是否允许原SQL。修改后即时生效，仅平台运营可用。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("tenant_id", mcp.Description("租户ID，留空=当前租户")),
			mcp.WithString("role", mcp.Required(), mcp.Description("角色: super_admin|region_manager|store_manager|staff|analyst")),
			mcp.WithString("data_scope", mcp.Required(), mcp.Description("数据范围: all|tenant|region|store")),
			mcp.WithArray("allowed_tables", mcp.Description("可访问表白名单")),
			mcp.WithBoolean("can_raw_sql", mcp.Description("是否允许执行原生SQL")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.permSet(ctx, req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("perm_delete",
			mcp.WithDescription("删除某租户级角色策略（回退到平台默认）。仅平台运营可用。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("tenant_id", mcp.Required(), mcp.Description("租户ID")),
			mcp.WithString("role", mcp.Required(), mcp.Description("角色")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.permDelete(ctx, req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("mask_view",
			mcp.WithDescription("查看数据脱敏规则（表.列 -> 脱敏类型）。可视化脱敏管理的只读视图，仅平台运营可用。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("tenant_id", mcp.Description("租户ID，留空=当前租户")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.maskView(ctx, req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("mask_set",
			mcp.WithDescription("设置/修改列级脱敏规则（phone/email/idcard/name/money/secret）。修改后即时生效，仅平台运营可用。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("tenant_id", mcp.Description("租户ID，留空=当前租户")),
			mcp.WithString("table", mcp.Required(), mcp.Description("表名")),
			mcp.WithString("column", mcp.Required(), mcp.Description("列名")),
			mcp.WithString("mask_type", mcp.Required(), mcp.Description("脱敏类型: phone|email|idcard|name|money|secret")),
			mcp.WithBoolean("enabled", mcp.Description("是否启用，默认true")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.maskSet(ctx, req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("mask_delete",
			mcp.WithDescription("删除某租户级脱敏规则。仅平台运营可用。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("tenant_id", mcp.Required(), mcp.Description("租户ID")),
			mcp.WithString("table", mcp.Required(), mcp.Description("表名")),
			mcp.WithString("column", mcp.Required(), mcp.Description("列名")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.maskDelete(ctx, req.GetArguments())), nil
		},
	)

	// --- 文件 / 目录读写工具（沙箱在 work_dir 内） ---
	s.AddTool(
		mcp.NewTool("read_file",
			mcp.WithDescription("读取文本文件内容。路径相对于工作目录（沙箱）。用于查看配置文件、日志、数据导出等。二进制文件可能乱码。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("path", mcp.Required(), mcp.Description("文件路径，相对于工作目录，如 reports/summary.txt")),
			mcp.WithNumber("max_bytes", mcp.Description("最多读取字节数，默认 65536，最大 1048576")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.readFile(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("write_file",
			mcp.WithDescription("写入文本文件（覆盖）。父目录不存在时自动创建。路径相对于工作目录。用于生成报告、导出分析结果。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("path", mcp.Required(), mcp.Description("文件路径，相对于工作目录，如 reports/summary.txt")),
			mcp.WithString("content", mcp.Required(), mcp.Description("要写入的文本内容")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.writeFile(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("append_file",
			mcp.WithDescription("向文本文件末尾追加内容（文件不存在则创建）。路径相对于工作目录。用于日志追加、结果累积。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("path", mcp.Required(), mcp.Description("文件路径，相对于工作目录")),
			mcp.WithString("content", mcp.Required(), mcp.Description("要追加的文本内容")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.appendFile(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("list_dir",
			mcp.WithDescription("列出目录下的文件和子目录（含名称、类型、大小、修改时间）。路径相对于工作目录，留空表示根目录。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("path", mcp.Description("目录路径，相对于工作目录，留空=根目录")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.listDir(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("make_dir",
			mcp.WithDescription("创建目录（含多级父目录）。路径相对于工作目录。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("path", mcp.Required(), mcp.Description("目录路径，相对于工作目录")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.makeDir(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("delete_file",
			mcp.WithDescription("删除一个文件（不会删除目录）。路径相对于工作目录。删除前请确认路径正确。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("path", mcp.Required(), mcp.Description("文件路径，相对于工作目录")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.deleteFile(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("read_dir_tree",
			mcp.WithDescription("递归列出目录树（最多两层，避免结果过大）。返回每个条目的相对路径与类型。用于了解工作目录整体结构。"),
			mcp.WithString("token", mcp.Required(), mcp.Description("登录令牌")),
			mcp.WithString("path", mcp.Description("起始目录，相对于工作目录，留空=根目录")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.readDirTree(req.GetArguments())), nil
		},
	)
}

// toolResult 把业务方法的 (interface{}, error) 转换为 MCP 工具结果。
// 业务错误通过 IsError=true 的结果回传给客户端（而非 Go error），以符合 MCP 规范。
func toolResult(v interface{}, err error) *mcp.CallToolResult {
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	b, e := json.Marshal(v)
	if e != nil {
		return mcp.NewToolResultError("序列化结果失败: " + e.Error())
	}
	return mcp.NewToolResultText(string(b))
}
