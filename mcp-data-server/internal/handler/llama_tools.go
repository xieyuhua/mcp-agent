package handler

import (
	"context"
	"fmt"
)

// LlamaToolCallRequest 供 llama.cpp 网页端调用工具时的请求体。
type LlamaToolCallRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// LlamaTool 返回 llama.cpp 可识别的 OpenAI 风格 function tool 定义。
type LlamaTool struct {
	Type     string            `json:"type"`
	Function LlamaToolFunction `json:"function"`
}

type LlamaToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// LlamaToolHandler 为 llama.cpp 网页端提供工具列表与执行能力。
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
				Name:        "query_table",
				Description: "结构化安全查询数据表。支持 customers / orders 等。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"table":   map[string]interface{}{"type": "string", "description": "表名: customers | orders"},
						"fields":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "返回字段，留空返回全部"},
						"filters": map[string]interface{}{"type": "object", "description": "等值过滤条件，如 {\"status\":\"paid\"}"},
						"order":   map[string]interface{}{"type": "string", "description": "排序，如 created_at desc"},
						"limit":   map[string]interface{}{"type": "integer", "description": "返回行数上限，默认100，最大1000"},
						"offset":  map[string]interface{}{"type": "integer", "description": "偏移"},
					},
					"required": []string{"table"},
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
						"table": map[string]interface{}{"type": "string", "description": "表名"},
					},
					"required": []string{"table"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "run_sql",
				Description: "执行原生只读 SQL（自动拦截危险关键字防注入）。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"sql": map[string]interface{}{"type": "string", "description": "SELECT 语句"},
					},
					"required": []string{"sql"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "read_file",
				Description: "读取文本文件内容。路径相对于工作目录（沙箱）。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":      map[string]interface{}{"type": "string", "description": "文件路径，相对于工作目录，如 reports/summary.txt"},
						"max_bytes": map[string]interface{}{"type": "integer", "description": "最多读取字节数，默认 65536，最大 1048576"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "write_file",
				Description: "写入文本文件（覆盖）。父目录不存在时自动创建。路径相对于工作目录。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":    map[string]interface{}{"type": "string", "description": "文件路径，相对于工作目录"},
						"content": map[string]interface{}{"type": "string", "description": "要写入的文本内容"},
					},
					"required": []string{"path", "content"},
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
						"path": map[string]interface{}{"type": "string", "description": "目录路径，相对于工作目录"},
					},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "web_search",
				Description: "联网搜索。返回相关网页的标题、链接与摘要。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{"type": "string", "description": "搜索关键词"},
						"limit": map[string]interface{}{"type": "integer", "description": "返回结果条数，默认5，最大10"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "web_fetch",
				Description: "抓取指定网页 URL 并提取正文纯文本。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"url":       map[string]interface{}{"type": "string", "description": "目标网页地址，需以 http:// 或 https:// 开头"},
						"max_chars": map[string]interface{}{"type": "integer", "description": "返回正文最大字符数，默认8000，最大40000"},
					},
					"required": []string{"url"},
				},
			},
		},
		{
			Type: "function",
			Function: LlamaToolFunction{
				Name:        "query_weather",
				Description: "查询指定城市的实时天气。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{"type": "string", "description": "城市名，如 北京、重庆、上海"},
					},
					"required": []string{"location"},
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

	switch req.Name {
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
	default:
		return nil, fmt.Errorf("unknown tool: %s", req.Name)
	}
}

// noopProgress 是用于 llama 兼容调用的空进度回调（非流式）。
func noopProgress(read int, message string) {}


