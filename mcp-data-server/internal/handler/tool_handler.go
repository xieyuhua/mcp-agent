package handler

import (
	"context"
	"fmt"

	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/service"
	"company.com/mcp-data-server/internal/tenant"
)

// ToolHandler 工具调用处理器，将 MCP 调用桥接到业务服务层。
type ToolHandler struct {
	auth       *service.AuthService
	query      *service.QueryService
	permission *service.PermissionService
}

func NewToolHandler(auth *service.AuthService, query *service.QueryService, permission *service.PermissionService) *ToolHandler {
	return &ToolHandler{auth: auth, query: query, permission: permission}
}

// Handle 实现 mcp.CallHandler。
func (h *ToolHandler) Handle(ctx context.Context, name string, args map[string]interface{}) (interface{}, error) {
	switch name {
	case "auth_login":
		return h.login(args)
	case "query_table":
		return h.queryTable(ctx, args)
	case "run_sql":
		return h.runSQL(ctx, args)
	case "describe_table":
		return h.describeTable(ctx, args)
	case "perm_view":
		return h.permView(ctx, args)
	case "perm_set":
		return h.permSet(ctx, args)
	case "perm_delete":
		return h.permDelete(ctx, args)
	case "mask_view":
		return h.maskView(ctx, args)
	case "mask_set":
		return h.maskSet(ctx, args)
	case "mask_delete":
		return h.maskDelete(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// ctxFromArgs 从参数中取出 token 并解析租户上下文。
func (h *ToolHandler) ctxFromArgs(args map[string]interface{}) (context.Context, *tenant.Context, error) {
	tok, _ := args["token"].(string)
	if tok == "" {
		return nil, nil, fmt.Errorf("missing token, please call auth_login first")
	}
	tc, err := h.auth.VerifyToken(tok)
	if err != nil {
		return nil, nil, fmt.Errorf("token invalid: %w", err)
	}
	return tenant.WithTenant(context.Background(), tc), tc, nil
}

func (h *ToolHandler) login(args map[string]interface{}) (interface{}, error) {
	username, _ := args["username"].(string)
	password, _ := args["password"].(string)
	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password are required")
	}
	tok, tc, err := h.auth.Login(username, password)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"token":      tok,
		"tenant_id":  tc.TenantID,
		"role":       tc.Role,
		"region_id":  tc.RegionID,
		"store_id":   tc.StoreID,
		"expires_in": "12h",
	}, nil
}

func (h *ToolHandler) queryTable(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	_, tc, err := h.ctxFromArgs(args)
	if err != nil {
		return nil, err
	}
	table, _ := args["table"].(string)
	if table == "" {
		return nil, fmt.Errorf("table is required")
	}
	req := repository.QueryRequest{
		Table:   table,
		Fields:  toStringSlice(args["fields"]),
		Filters: toStringMap(args["filters"]),
		Order:   optString(args["order"]),
		Limit:   optInt(args["limit"]),
		Offset:  optInt(args["offset"]),
	}
	return h.query.QueryTable(ctx, tc, req)
}

func (h *ToolHandler) runSQL(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	_, tc, err := h.ctxFromArgs(args)
	if err != nil {
		return nil, err
	}
	sql, _ := args["sql"].(string)
	if sql == "" {
		return nil, fmt.Errorf("sql is required")
	}
	return h.query.RunSQL(ctx, tc, sql)
}

func (h *ToolHandler) describeTable(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	_, tc, err := h.ctxFromArgs(args)
	if err != nil {
		return nil, err
	}
	table, _ := args["table"].(string)
	if table == "" {
		return nil, fmt.Errorf("table is required")
	}
	cols, err := h.query.DescribeTable(tc, table)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"table": table, "columns": cols}, nil
}

// ---- 权限可视化设置管理 ----
// 这些工具在 PermissionService 内已做 super_admin 鉴权与审计。

func (h *ToolHandler) permView(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	_, tc, err := h.ctxFromArgs(args)
	if err != nil {
		return nil, err
	}
	tenantID := optString(args["tenant_id"])
	views, err := h.permission.ListPolicies(tc, tenantID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"policies": views}, nil
}

func (h *ToolHandler) permSet(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	_, tc, err := h.ctxFromArgs(args)
	if err != nil {
		return nil, err
	}
	req := service.SetPolicyRequest{
		TenantID:     optString(args["tenant_id"]),
		Role:         optString(args["role"]),
		DataScope:    optString(args["data_scope"]),
		AllowedTables: toStringSlice(args["allowed_tables"]),
		CanRawSQL:    optBool(args["can_raw_sql"]),
	}
	view, err := h.permission.SetPolicy(tc, req)
	if err != nil {
		return nil, err
	}
	return view, nil
}

func (h *ToolHandler) permDelete(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	_, tc, err := h.ctxFromArgs(args)
	if err != nil {
		return nil, err
	}
	if err := h.permission.DeletePolicy(tc, optString(args["tenant_id"]), optString(args["role"])); err != nil {
		return nil, err
	}
	return map[string]interface{}{"deleted": true}, nil
}

func (h *ToolHandler) maskView(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	_, tc, err := h.ctxFromArgs(args)
	if err != nil {
		return nil, err
	}
	tenantID := optString(args["tenant_id"])
	views, err := h.permission.ListMaskRules(tc, tenantID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"rules": views}, nil
}

func (h *ToolHandler) maskSet(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	_, tc, err := h.ctxFromArgs(args)
	if err != nil {
		return nil, err
	}
	req := service.SetMaskRuleRequest{
		TenantID: optString(args["tenant_id"]),
		Table:    optString(args["table"]),
		Column:   optString(args["column"]),
		MaskType: optString(args["mask_type"]),
		Enabled:  optBoolDefault(args["enabled"], true),
	}
	view, err := h.permission.SetMaskRule(tc, req)
	if err != nil {
		return nil, err
	}
	return view, nil
}

func (h *ToolHandler) maskDelete(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	_, tc, err := h.ctxFromArgs(args)
	if err != nil {
		return nil, err
	}
	if err := h.permission.DeleteMaskRule(tc, optString(args["tenant_id"]), optString(args["table"]), optString(args["column"])); err != nil {
		return nil, err
	}
	return map[string]interface{}{"deleted": true}, nil
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

func optBool(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

// optBoolDefault 若参数未提供则使用默认值 def。
func optBoolDefault(v interface{}, def bool) bool {
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
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
