package tenant

import "context"

// CtxKey 上下文键类型，避免冲突。
type CtxKey string

const tenantKey CtxKey = "tenant_context"

// Context 一次请求中解析出的租户/身份上下文，贯穿所有业务层。
type Context struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
	RegionID string `json:"region_id"`
	StoreID  string `json:"store_id"`
}

// WithTenant 将租户上下文注入 context。
func WithTenant(ctx context.Context, t *Context) context.Context {
	return context.WithValue(ctx, tenantKey, t)
}

// FromContext 从 context 取出租户上下文。
func FromContext(ctx context.Context) (*Context, bool) {
	t, ok := ctx.Value(tenantKey).(*Context)
	return t, ok
}
