package model

import (
	"time"
)

// PermissionPolicy 角色级权限策略（可可视化配置，替代硬编码）。
// 每个租户可独立配置自己的角色策略；super_admin 还可配置平台级（TenantID=""）。
type PermissionPolicy struct {
	ID            uint   `gorm:"primaryKey"`
	TenantID      string `gorm:"uniqueIndex:idx_perm_tenant_role"` // 空串表示平台全局默认
	Role          string `gorm:"size:32;uniqueIndex:idx_perm_tenant_role"`
	DataScope     string `gorm:"size:16"`   // all|tenant|region|store
	AllowedTables string `gorm:"type:text"` // 逗号分隔的表白名单
	CanRawSQL     bool   `gorm:"default:false"`
	UpdatedBy     string `gorm:"size:64"`
	UpdatedAt     time.Time
}

// MaskRule 列级脱敏规则（可可视化配置）。
// 每个租户可独立配置；TenantID="" 表示平台全局默认。
type MaskRule struct {
	ID        uint   `gorm:"primaryKey"`
	TenantID  string `gorm:"uniqueIndex:idx_mask_tenant_table_col"`
	TableName string `gorm:"size:64;uniqueIndex:idx_mask_tenant_table_col"`
	Column    string `gorm:"size:64;uniqueIndex:idx_mask_tenant_table_col"`
	MaskType  string `gorm:"size:16"` // phone|email|idcard|name|money|secret
	Enabled   bool   `gorm:"not null"`
	UpdatedBy string `gorm:"size:64"`
	UpdatedAt time.Time
}

// FieldPermission 字段级权限（按角色控制表列是否可见）。
// 每个租户可独立配置；TenantID="" 表示平台全局默认。
// Hidden=true 表示该角色在该租户下看不到该字段（describe_table 不返回、查询结果不展示、SQL 执行后过滤掉）。
type FieldPermission struct {
	ID        uint   `gorm:"primaryKey"`
	TenantID  string `gorm:"size:64;uniqueIndex:idx_field_perm_tenant_role_table_col"` // 空串表示平台全局默认
	Role      string `gorm:"size:32;uniqueIndex:idx_field_perm_tenant_role_table_col"` // super_admin|region_manager|...
	TableName string `gorm:"size:64;uniqueIndex:idx_field_perm_tenant_role_table_col"` // 表名
	Column    string `gorm:"size:64;uniqueIndex:idx_field_perm_tenant_role_table_col"` // 列名
	Hidden    bool   `gorm:"not null;default:true"`                                    // true=隐藏，false=显式可见（用于覆盖平台默认）
	UpdatedBy string `gorm:"size:64"`
	UpdatedAt time.Time
}

// ParseHiddenFieldsMap 把字段权限列表合并为 table -> column -> hidden 的嵌套 map。
// 租户级记录后写入，会覆盖平台默认。
func ParseHiddenFieldsMap(rules []FieldPermission) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, r := range rules {
		if out[r.TableName] == nil {
			out[r.TableName] = map[string]bool{}
		}
		out[r.TableName][r.Column] = r.Hidden
	}
	return out
}

// Role 动态角色定义（super_admin 可在后台新增/修改）。
// 内置角色（is_builtin=true）不可删除，仅可修改描述。
type Role struct {
	ID          uint   `gorm:"primaryKey"`
	TenantID    string `gorm:"size:36;index:idx_role_tenant"`            // 空串表示平台全局角色
	Name        string `gorm:"size:32;uniqueIndex:idx_role_tenant_name"` // 角色标识，如 custom_ops
	DisplayName string `gorm:"size:128"`                                 // 显示名称，如 "运营专员"
	Description string `gorm:"type:text"`                                // 描述
	IsBuiltin   bool   `gorm:"not null;default:false"`                   // 是否内置角色
	UpdatedBy   string `gorm:"size:64"`
	UpdatedAt   time.Time
}

// TableName 显式指定表名。
func (Role) TableName() string { return "roles" }

// 数据范围合法值（用于可视化下拉 / 校验）。
var ValidScopes = []string{"all", "tenant", "region", "store"}

// 脱敏类型合法值（用于可视化下拉 / 校验）。
var ValidMaskTypes = []string{"phone", "email", "idcard", "name", "money", "secret"}
