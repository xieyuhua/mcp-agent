package service

// QueryContext 查询上下文（替代原 tenant 包）。
type QueryContext struct {
	TenantID string
	UserID   string
	Role     string
	RegionID string
	StoreID  string
}
