package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"company.com/mcp-data-server/internal/service"
)

// LlamaToolCallRequest 供 llama.cpp 网页端调用工具时的请求体。
// 支持两种方式鉴权：
//   1. 直接传 token（已先调用 /api/llama/login 登录）；
//   2. 每次调用传 username/password 自动登录（适合简单演示）。
type LlamaToolCallRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
	Token     string                 `json:"token"`
	Username  string                 `json:"username"`
	Password  string                 `json:"password"`
}

// LlamaTool 返回 llama.cpp 可识别的 OpenAI 风格 function tool 定义。
type LlamaTool struct {
	Type     string          `json:"type"`
	Function LlamaToolFunction `json:"function"`
}

type LlamaToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// LlamaToolHandler 为 llama.cpp 网页端提供工具列表与执行能力。
// 它复用 ToolHandler 的登录/鉴权/业务逻辑，但在接口层使用 OpenAI 风格的 function schema。
type LlamaToolHandler struct {
	h *ToolHandler
}

func NewLlamaToolHandler(h *ToolHandler) *LlamaToolHandler {
	return &LlamaToolHandler{h: h}
}

// ListTools 返回 llama.cpp 网页可加载的工具清单（OpenAI function calling 格式）。
func (l *LlamaToolHandler) ListTools() []LlamaTool {
	return []LlamaTool{
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "auth_login",
				Description: "使用用户名/密码登录，获取后续工具调用所需的 token。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"username": map[string]interface{}{"type": "string", "description": "用户名"},
						"password": map[string]interface{}{"type": "string", "description": "密码"},
					},
					"required": []string{"username", "password"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "query_table",
				Description: "结构化安全查询数据表。自动叠加租户/区域/门店隔离，并对敏感字段脱敏。支持 customers / orders 等。",
				Parameters: map[string]interface{}{
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
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "describe_table",
				Description: "查看数据表的字段结构（供数据分析师了解 schema）。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
						"table": map[string]interface{}{"type": "string", "description": "表名"},
					},
					"required": []string{"token", "table"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "run_sql",
				Description: "执行原生只读 SQL（仅平台运营 super_admin 可用，自动拦截危险关键字防注入）。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
						"sql":   map[string]interface{}{"type": "string", "description": "SELECT 语句"},
					},
					"required": []string{"token", "sql"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "read_file",
				Description: "读取文本文件内容。路径相对于工作目录（沙箱）。用于查看配置文件、日志、数据导出等。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"token":     map[string]interface{}{"type": "string", "description": "登录令牌"},
						"path":      map[string]interface{}{"type": "string", "description": "文件路径，相对于工作目录，如 reports/summary.txt"},
						"max_bytes": map[string]interface{}{"type": "integer", "description": "最多读取字节数，默认 65536，最大 1048576"},
					},
					"required": []string{"token", "path"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "write_file",
				Description: "写入文本文件（覆盖）。父目录不存在时自动创建。路径相对于工作目录。用于生成报告、导出分析结果。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"token":   map[string]interface{}{"type": "string", "description": "登录令牌"},
						"path":    map[string]interface{}{"type": "string", "description": "文件路径，相对于工作目录"},
						"content": map[string]interface{}{"type": "string", "description": "要写入的文本内容"},
					},
					"required": []string{"token", "path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "list_dir",
				Description: "列出目录下的文件和子目录。路径相对于工作目录，留空表示根目录。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
						"path":  map[string]interface{}{"type": "string", "description": "目录路径，相对于工作目录"},
					},
					"required": []string{"token"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "web_search",
				Description: "联网搜索。返回相关网页的标题、链接与摘要，用于获取实时/外部信息。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"token": map[string]interface{}{"type": "string", "description": "登录令牌"},
						"query": map[string]interface{}{"type": "string", "description": "搜索关键词，如「2024 年中国 GDP 增速」"},
						"limit": map[string]interface{}{"type": "integer", "description": "返回结果条数，默认5，最大10"},
					},
					"required": []string{"token", "query"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "web_fetch",
				Description: "抓取指定网页 URL 并提取正文纯文本。用于读取搜索结果的具体内容、新闻、文档。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"token":     map[string]interface{}{"type": "string", "description": "登录令牌"},
						"url":       map[string]interface{}{"type": "string", "description": "目标网页地址，需以 http:// 或 https:// 开头"},
						"max_chars": map[string]interface{}{"type": "integer", "description": "返回正文最大字符数，默认8000，最大40000"},
					},
					"required": []string{"token", "url"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "query_weather",
				Description: "查询指定城市的实时天气（温度、体感温度、天气状况、湿度、气压、风速）。当用户询问天气、气温、穿衣建议等问题时使用。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"token":    map[string]interface{}{"type": "string", "description": "登录令牌"},
						"location": map[string]interface{}{"type": "string", "description": "城市名，如 北京、重庆、上海"},
					},
					"required": []string{"token", "location"},
				},
			},
		},
	}
}

// CallTool 执行一次 llama.cpp 风格的 function call，并返回执行结果。
func (l *LlamaToolHandler) CallTool(ctx context.Context, req LlamaToolCallRequest) (interface{}, error) {
	if req.Arguments == nil {
		req.Arguments = map[string]interface{}{}
	}

	// 鉴权：优先使用显式 token；否则用 username/password 自动登录。
	if req.Token != "" {
		req.Arguments["token"] = req.Token
	} else if req.Name != "auth_login" {
		if req.Username == "" || req.Password == "" {
			return nil, fmt.Errorf("token or username/password required")
		}
		loginRes, err := l.h.login(map[string]interface{}{"username": req.Username, "password": req.Password})
		if err != nil {
			return nil, fmt.Errorf("login failed: %w", err)
		}
		m, ok := loginRes.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("unexpected login response type")
		}
		req.Arguments["token"] = m["token"]
	}

	// 工具分发。
	switch req.Name {
	case "auth_login":
		return l.h.login(req.Arguments)
	case "query_table":
		return l.h.queryTable(ctx, req.Arguments, noopProgress)
	case "describe_table":
		return l.h.describeTable(ctx, req.Arguments)
	case "run_sql":
		return l.h.runSQL(ctx, req.Arguments, noopProgress)
	case "read_file":
		return l.h.readFile(req.Arguments)
	case "write_file":
		return l.h.writeFile(req.Arguments)
	case "append_file":
		return l.h.appendFile(req.Arguments)
	case "list_dir":
		return l.h.listDir(req.Arguments)
	case "make_dir":
		return l.h.makeDir(req.Arguments)
	case "delete_file":
		return l.h.deleteFile(req.Arguments)
	case "read_dir_tree":
		return l.h.readDirTree(req.Arguments)
	case "web_search":
		return l.h.webSearch(ctx, req.Arguments, noopProgress)
	case "web_fetch":
		return l.h.webFetch(ctx, req.Arguments, noopProgress)
	case "query_weather":
		return l.h.queryWeather(ctx, req.Arguments, noopProgress)
	case "perm_view":
		return l.h.permView(ctx, req.Arguments)
	case "perm_set":
		return l.h.permSet(ctx, req.Arguments)
	case "perm_delete":
		return l.h.permDelete(ctx, req.Arguments)
	case "mask_view":
		return l.h.maskView(ctx, req.Arguments)
	case "mask_set":
		return l.h.maskSet(ctx, req.Arguments)
	case "mask_delete":
		return l.h.maskDelete(ctx, req.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", req.Name)
	}
}

// noopProgress 是用于 llama 兼容调用的空进度回调（非流式）。
func noopProgress(read int, message string) {}

// MarshalJSON 在需要把结果文本化时提供便利。
func (l *LlamaToolHandler) MarshalJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ service.ProgressFunc = noopProgress
