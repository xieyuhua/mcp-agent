package model

import "time"

// PermissionPolicy 角色级权限策略（可可视化配置，替代硬编码）。
// 每个租户可独立配置自己的角色策略；super_admin 还可配置平台级（TenantID=""）。
type PermissionPolicy struct {
	ID          uint      `gorm:"primaryKey"`
	TenantID    string    `gorm:"uniqueIndex:idx_perm_tenant_role"` // 空串表示平台全局默认
	Role        string    `gorm:"size:32;uniqueIndex:idx_perm_tenant_role"`
	DataScope   string    `gorm:"size:16"`  // all|tenant|region|store
	AllowedTables string  `gorm:"type:text"` // 逗号分隔的表白名单
	CanRawSQL   bool      `gorm:"default:false"`
	UpdatedBy   string    `gorm:"size:64"`
	UpdatedAt   time.Time
}

// MaskRule 列级脱敏规则（可可视化配置）。
// 每个租户可独立配置；TenantID="" 表示平台全局默认。
type MaskRule struct {
	ID        uint      `gorm:"primaryKey"`
	TenantID  string    `gorm:"uniqueIndex:idx_mask_tenant_table_col"`
	TableName string    `gorm:"size:64;uniqueIndex:idx_mask_tenant_table_col"`
	Column    string    `gorm:"size:64;uniqueIndex:idx_mask_tenant_table_col"`
	MaskType  string    `gorm:"size:16"` // phone|email|idcard|name|money|secret
	Enabled   bool      `gorm:"not null"`
	UpdatedBy string    `gorm:"size:64"`
	UpdatedAt time.Time
}

// 数据范围合法值（用于可视化下拉 / 校验）。
var ValidScopes = []string{"all", "tenant", "region", "store"}

// 脱敏类型合法值（用于可视化下拉 / 校验）。
var ValidMaskTypes = []string{"phone", "email", "idcard", "name", "money", "secret"}
