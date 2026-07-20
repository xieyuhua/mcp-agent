package model

import "time"

// Tenant 租户（SaaS 隔离根）。
type Tenant struct {
	ID        string    `gorm:"primaryKey;size:36"`
	Name      string    `gorm:"size:128"`
	CreatedAt time.Time
}

// Customer 业务表：客户。
type Customer struct {
	ID        uint      `gorm:"primaryKey"`
	TenantID  string    `gorm:"index:idx_cust_tenant"`
	RegionID  string    `gorm:"index:idx_cust_region"`
	StoreID   string    `gorm:"index:idx_cust_store"`
	Name      string    `gorm:"size:64"`
	Phone     string    `gorm:"size:32"`
	Email     string    `gorm:"size:128"`
	IDCard    string    `gorm:"size:32"`
	CreatedAt time.Time
}

// Order 业务表：订单。
type Order struct {
	ID         uint      `gorm:"primaryKey"`
	TenantID   string    `gorm:"index:idx_ord_tenant"`
	RegionID   string    `gorm:"index:idx_ord_region"`
	StoreID    string    `gorm:"index:idx_ord_store"`
	CustomerID uint
	Amount     float64
	Status     string `gorm:"size:32"`
	CreatedAt  time.Time
}

// AuditLog 审计日志：记录每一次工具调用与查询。
type AuditLog struct {
	ID        uint      `gorm:"primaryKey"`
	TenantID  string    `gorm:"index:idx_audit_tenant"`
	UserID    string    `gorm:"index:idx_audit_user"`
	Action    string    `gorm:"size:64"`
	Tool      string    `gorm:"size:64"`
	TableName string    `gorm:"size:64"`
	Query     string    `gorm:"type:text"`
	RowCount  int
	IP        string    `gorm:"size:64"`
	CreatedAt time.Time
}
