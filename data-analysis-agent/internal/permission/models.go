package permission

import "time"

// PermissionPolicy 角色级权限策略（可可视化配置）。
type PermissionPolicy struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	TenantID      string    `gorm:"uniqueIndex:idx_perm_tenant_role" json:"tenant_id"`
	Role          string    `gorm:"size:32;uniqueIndex:idx_perm_tenant_role" json:"role"`
	DataScope     string    `gorm:"size:16" json:"data_scope"`    // all|tenant|region|store
	AllowedTables string    `gorm:"type:text" json:"allowed_tables"` // 逗号分隔
	CanRawSQL     bool      `gorm:"default:false" json:"can_raw_sql"`
	UpdatedBy     string    `gorm:"size:64" json:"updated_by"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// MaskRule 列级脱敏规则。
type MaskRule struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TenantID  string    `gorm:"uniqueIndex:idx_mask_tenant_table_col" json:"tenant_id"`
	TableName string    `gorm:"size:64;uniqueIndex:idx_mask_tenant_table_col" json:"table_name"`
	Column    string    `gorm:"size:64;uniqueIndex:idx_mask_tenant_table_col" json:"column"`
	MaskType  string    `gorm:"size:16" json:"mask_type"` // phone|email|idcard|name|money|secret
	Enabled   bool      `gorm:"not null" json:"enabled"`
	UpdatedBy string    `gorm:"size:64" json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FieldPermission 字段级权限（按角色控制表列是否可见）。
type FieldPermission struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TenantID  string    `gorm:"size:64;uniqueIndex:idx_field_perm_tenant_role_table_col" json:"tenant_id"`
	Role      string    `gorm:"size:32;uniqueIndex:idx_field_perm_tenant_role_table_col" json:"role"`
	TableName string    `gorm:"size:64;uniqueIndex:idx_field_perm_tenant_role_table_col" json:"table_name"`
	Column    string    `gorm:"size:64;uniqueIndex:idx_field_perm_tenant_role_table_col" json:"column"`
	Hidden    bool      `gorm:"not null;default:true" json:"hidden"`
	UpdatedBy string    `gorm:"size:64" json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ValidScopes 数据范围合法值。
var ValidScopes = []string{"all", "tenant", "region", "store"}

// ValidMaskTypes 脱敏类型合法值。
var ValidMaskTypes = []string{"phone", "email", "idcard", "name", "money", "secret"}

// ParseHiddenFieldsMap 把字段权限列表合并为 table -> column -> hidden 的嵌套 map。
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
