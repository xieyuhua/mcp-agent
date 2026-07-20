package handler

import (
	"context"
	"strings"

	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/service"
)

// ToolHandler 工具调用处理器，将 MCP 调用桥接到业务服务层。
type ToolHandler struct {
	query   *service.QueryService
	web     *service.WebService
	weather *service.WeatherService
	workDir string
	sandbox bool
}

func NewToolHandler(query *service.QueryService, web *service.WebService, weather *service.WeatherService, workDir string, sandbox bool) *ToolHandler {
	return &ToolHandler{query: query, web: web, weather: weather, workDir: workDir, sandbox: sandbox}
}

// ctx 返回一个 super_admin 查询上下文。
func (h *ToolHandler) ctx() *service.QueryContext {
	return &service.QueryContext{
		TenantID: "",
		UserID:   "agent",
		Role:     "super_admin",
	}
}

func (h *ToolHandler) queryTable(ctx context.Context, args map[string]interface{}, onProgress service.ProgressFunc) (interface{}, error) {
	tc := h.ctx()
	table, _ := args["table"].(string)
	if table == "" {
		return nil, nil
	}
	req := repository.QueryRequest{
		Table:   table,
		Fields:  toStringSlice(args["fields"]),
		Filters: toStringMap(args["filters"]),
		Order:   optString(args["order"]),
		Limit:   optInt(args["limit"]),
		Offset:  optInt(args["offset"]),
	}
	return h.query.QueryTable(ctx, tc, req, onProgress)
}

func (h *ToolHandler) runSQL(ctx context.Context, args map[string]interface{}, onProgress service.ProgressFunc) (interface{}, error) {
	tc := h.ctx()
	sql, _ := args["sql"].(string)
	if sql == "" {
		sql, _ = args["query"].(string)
	}
	if sql == "" {
		return nil, nil
	}
	return h.query.RunSQL(ctx, tc, sql, onProgress)
}

func (h *ToolHandler) describeTable(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	tc := h.ctx()
	table, _ := args["table"].(string)
	if table == "" {
		return nil, nil
	}
	cols, err := h.query.DescribeTable(tc, table)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"table": table, "columns": cols}, nil
}

// ---- 联网查询工具 ----

func (h *ToolHandler) webSearch(ctx context.Context, args map[string]interface{}, onProgress service.ProgressFunc) (interface{}, error) {
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	return h.web.Search(ctx, query, optInt(args["limit"]), onProgress)
}

func (h *ToolHandler) webFetch(ctx context.Context, args map[string]interface{}, onProgress service.ProgressFunc) (interface{}, error) {
	targetURL, _ := args["url"].(string)
	if strings.TrimSpace(targetURL) == "" {
		return nil, nil
	}
	return h.web.Fetch(ctx, targetURL, optInt(args["max_chars"]), onProgress)
}

func (h *ToolHandler) queryWeather(ctx context.Context, args map[string]interface{}, onProgress service.ProgressFunc) (interface{}, error) {
	location, _ := args["location"].(string)
	if strings.TrimSpace(location) == "" {
		return nil, nil
	}
	return h.weather.Query(ctx, location, onProgress)
}

// ---- 参数转换辅助 ----

func toStringSlice(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toStringMap(v interface{}) map[string]interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	return m
}

func optString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func optInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}
