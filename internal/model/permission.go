package model

import "time"

// TableConfig 后台预配置的「表结构」元数据。
// 大模型不再直连数据库探测 schema，而是读取这里配置的表注释与字段，
// 从而按业务语义生成 SQL。
type TableConfig struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:64;uniqueIndex" json:"name"`    // 物理表名
	Title     string    `gorm:"size:128" json:"title"`              // 业务名称（中文）
	Comment   string    `gorm:"type:text" json:"comment"`           // 表注释/业务说明，供大模型理解
	// 注意：布尔列不使用 gorm default 标签。GORM 在 Create 时会忽略 Go 零值 false，
	// 若列上带 default:true，写入 false 会被数据库默认值覆盖成 true，导致「无法停用」。
	Enabled   bool      `json:"enabled"` // 是否对 agent 开放
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FieldConfig 表字段配置：字段注释（供大模型）+ 字段级权限（按角色可见/脱敏）。
// (table_name, name) 组合唯一，支持从数据库导入时按表名+字段名 upsert（注释不覆盖已有）。
type FieldConfig struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TableName string    `gorm:"size:64;uniqueIndex:idx_field_uniq" json:"table_name"`
	Name      string    `gorm:"size:64;uniqueIndex:idx_field_uniq" json:"name"` // 字段名
	Title     string    `gorm:"size:128" json:"title"`       // 业务名称
	DataType  string    `gorm:"size:32" json:"data_type"`    // 数据类型，如 varchar/int
	Comment   string    `gorm:"type:text" json:"comment"`    // 字段注释，供大模型理解
	Sensitive bool      `json:"sensitive"`                  // 是否敏感字段（默认需脱敏）
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableRelation 表关联关系：供大模型理解如何 JOIN，也用于校验。
// 关联条件优先使用 OnExpr（自由 ON 表达式，支持多字段与任意条件，如
// a.uid = b.uid AND a.tenant_id = b.tenant_id）。
// LeftTable/RightTable 仅用于列表展示与自动建议；JoinType 决定拼接时的 JOIN 类型。
type TableRelation struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	LeftTable  string    `gorm:"size:64;index" json:"left_table"` // 左表
	RightTable string    `gorm:"size:64;index" json:"right_table"` // 右表
	OnExpr     string    `gorm:"type:text" json:"on_expr"`         // 自由 ON 表达式（核心）
	LeftColumn  string    `gorm:"size:64" json:"left_column"`        // 便捷项：单字段关联左列（可选）
	RightColumn string    `gorm:"size:64" json:"right_column"`      // 便捷项：单字段关联右列（可选）
	JoinType    string    `gorm:"size:16;default:'INNER'" json:"join_type"` // INNER/LEFT/RIGHT
	Comment     string    `gorm:"type:text" json:"comment"`          // 关系说明
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RolePolicy 角色数据权限：某个 origin_role 对某张表的行级 where 条件。
// Condition 支持 {alias} 占位符，引擎注入时自动替换为 SQL 中的实际别名。
// 例如：{alias}.tenant_id = 't1' AND {alias}.region_id IN ('r1','r2')
type RolePolicy struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Role      string    `gorm:"size:64;index:idx_policy_role" json:"role"`       // origin_role
	TableName string    `gorm:"size:64;index:idx_policy_table" json:"table_name"` // 目标表
	Condition string    `gorm:"type:text" json:"condition"`                      // where 片段模板
	Enabled   bool      `json:"enabled"` // 不用 gorm default，理由同 TableConfig.Enabled
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RoleFieldGrant 角色字段权限：控制某角色对某表哪些字段可见/需脱敏。
// 若某表未配置任何 RoleFieldGrant，则默认全部字段可见（敏感字段按 FieldConfig.Sensitive 脱敏）。
type RoleFieldGrant struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Role      string    `gorm:"size:64;index:idx_grant_role" json:"role"`
	TableName string    `gorm:"size:64;index:idx_grant_table" json:"table_name"`
	Field     string    `gorm:"size:64" json:"field"`
	Visible   bool      `json:"visible"` // 是否可见（不用 gorm default，否则 false 会被覆盖）
	Masked    bool      `json:"masked"`  // 是否脱敏返回
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Role 角色定义（origin_role 字典）。
type Role struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Code      string    `gorm:"size:64;uniqueIndex" json:"code"` // origin_role 值
	Name      string    `gorm:"size:128" json:"name"`            // 显示名
	Remark    string    `gorm:"type:text" json:"remark"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AdminUser 后台登录账号。
// 密码不直接存储明文，而是存 sha256(salt+password) 与随机 salt。
// Role 标识账号在后台的权限级别，admin 为超级管理员。
type AdminUser struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"size:64;uniqueIndex" json:"username"`
	PasswordHash string    `gorm:"size:128" json:"-"` // 不对外暴露
	Salt         string    `gorm:"size:64" json:"-"`  // 不对外暴露
	DisplayName  string    `gorm:"size:128" json:"display_name"`
	Role         string    `gorm:"size:32" json:"role"` // admin | operator
	MustChange   bool      `json:"must_change"`         // 是否必须改密（首次安装为 true）
	SessionVersion int64   `json:"-"`                   // 会话版本号：退出/改密时递增，使所有旧 token 立即失效
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
