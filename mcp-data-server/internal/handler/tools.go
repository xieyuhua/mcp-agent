package handler

import (
	"context"
	"encoding/json"

	"company.com/mcp-data-server/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTools 把全部业务工具注册到 mcp-go 的 MCP 服务实例上。
func RegisterTools(s *server.MCPServer, h *ToolHandler) {
	// --- 数据查询 ---
	s.AddTool(
		mcp.NewTool("query_table",
			mcp.WithDescription("结构化安全查询数据表。支持 customers / orders。"),
			mcp.WithString("table", mcp.Required(), mcp.Description("表名: customers | orders")),
			mcp.WithArray("fields", mcp.Description("返回字段，留空返回全部")),
			mcp.WithObject("filters", mcp.Description("等值过滤条件，如 {\"status\":\"paid\"}")),
			mcp.WithString("order", mcp.Description("排序，如 created_at desc")),
			mcp.WithNumber("limit", mcp.Description("返回行数上限，默认100，最大1000")),
			mcp.WithNumber("offset", mcp.Description("偏移")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.queryTable(ctx, req.GetArguments(), makeProgressSender(s, ctx))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("run_sql",
			mcp.WithDescription("执行原生只读 SQL（自动拦截危险关键字防注入）。"),
			mcp.WithString("sql", mcp.Required(), mcp.Description("SELECT 语句")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.runSQL(ctx, req.GetArguments(), makeProgressSender(s, ctx))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("describe_table",
			mcp.WithDescription("查看数据表的字段结构（供数据分析师了解 schema）。"),
			mcp.WithString("table", mcp.Required(), mcp.Description("表名")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.describeTable(ctx, req.GetArguments())), nil
		},
	)

	// --- 文件 / 目录读写工具（沙箱在 work_dir 内） ---
	s.AddTool(
		mcp.NewTool("read_file",
			mcp.WithDescription("读取文本文件内容。路径相对于工作目录（沙箱）。"),
			mcp.WithString("path", mcp.Required(), mcp.Description("文件路径，相对于工作目录，如 reports/summary.txt")),
			mcp.WithNumber("max_bytes", mcp.Description("最多读取字节数，默认 65536，最大 1048576")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.readFile(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("write_file",
			mcp.WithDescription("写入文本文件（覆盖）。父目录不存在时自动创建。路径相对于工作目录。"),
			mcp.WithString("path", mcp.Required(), mcp.Description("文件路径，相对于工作目录，如 reports/summary.txt")),
			mcp.WithString("content", mcp.Required(), mcp.Description("要写入的文本内容")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.writeFile(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("append_file",
			mcp.WithDescription("向文本文件末尾追加内容（文件不存在则创建）。路径相对于工作目录。"),
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
			mcp.WithString("path", mcp.Description("目录路径，相对于工作目录，留空=根目录")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.listDir(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("make_dir",
			mcp.WithDescription("创建目录（含多级父目录）。路径相对于工作目录。"),
			mcp.WithString("path", mcp.Required(), mcp.Description("目录路径，相对于工作目录")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.makeDir(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("delete_file",
			mcp.WithDescription("删除一个文件（不会删除目录）。路径相对于工作目录。"),
			mcp.WithString("path", mcp.Required(), mcp.Description("文件路径，相对于工作目录")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.deleteFile(req.GetArguments())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("read_dir_tree",
			mcp.WithDescription("递归列出目录树（最多两层，避免结果过大）。"),
			mcp.WithString("path", mcp.Description("起始目录，相对于工作目录，留空=根目录")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.readDirTree(req.GetArguments())), nil
		},
	)

	// --- 联网查询工具 ---
	s.AddTool(
		mcp.NewTool("web_search",
			mcp.WithDescription("联网搜索。返回相关网页的标题、链接与摘要。"),
			mcp.WithString("query", mcp.Required(), mcp.Description("搜索关键词")),
			mcp.WithNumber("limit", mcp.Description("返回结果条数，默认5，最大10")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.webSearch(ctx, req.GetArguments(), makeProgressSender(s, ctx))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("web_fetch",
			mcp.WithDescription("抓取指定网页 URL 并提取正文纯文本。"),
			mcp.WithString("url", mcp.Required(), mcp.Description("目标网页地址，需以 http:// 或 https:// 开头")),
			mcp.WithNumber("max_chars", mcp.Description("返回正文最大字符数，默认8000，最大40000")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.webFetch(ctx, req.GetArguments(), makeProgressSender(s, ctx))), nil
		},
	)

	// --- 天气查询 ---
	s.AddTool(
		mcp.NewTool("query_weather",
			mcp.WithDescription("查询指定城市的实时天气（温度、体感温度、天气状况、湿度、气压、风速）。"),
			mcp.WithString("location", mcp.Required(), mcp.Description("城市名，如 北京、重庆、上海")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return toolResult(h.queryWeather(ctx, req.GetArguments(), makeProgressSender(s, ctx))), nil
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

// makeProgressSender 返回一个 service.ProgressFunc，在工具执行期间把进度转换为
// MCP notifications/progress 推送给当前客户端，实现「分析过程」流式展示。
// 任何发送失败都被静默忽略，绝不影响主流程（避免 progress 导致工具报错）。
func makeProgressSender(s *server.MCPServer, ctx context.Context) service.ProgressFunc {
	return func(read int, message string) {
		defer func() { _ = recover() }()
		_ = s.SendNotificationToClient(ctx, "notifications/progress", map[string]any{
			"progress": read,
			"message":  message,
		})
	}
}
