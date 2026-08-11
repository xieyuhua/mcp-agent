package repository

import (
	"strings"
	"time"

	"company.com/mcp-data-server/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IntrospectedField 从数据库读取到的真实字段信息。
type IntrospectedField struct {
	TableName  string
	ColumnName string
	DataType   string
	IsPrimaryKey bool
}

// IntrospectSchema 读取真实数据库中所有用户表的表名与字段（表名、列名、类型）。
// 返回按表分组的字段列表，便于批量导入为 TableConfig / FieldConfig。
// 该操作只读 information_schema / sqlite_master，不经过权限层，也不会执行用户 SQL。
func (r *PermissionRepo) IntrospectSchema() (map[string][]IntrospectedField, error) {
	tables, err := r.db.Migrator().GetTables()
	if err != nil {
		return nil, err
	}

	// 过滤掉本系统自身的权限元数据表，避免把后台配置暴露给 agent。
	// 注意只跳过元数据表——用户的真实业务表（如 customers/orders）应正常导入。
	// 表名统一转小写比较，兼容 Oracle 默认大写返回。
	skip := map[string]bool{
		"roles": true, "table_configs": true, "field_configs": true,
		"table_relations": true, "role_policies": true, "role_field_grants": true,
		"admin_users": true, "audit_logs": true,
	}

	out := make(map[string][]IntrospectedField)
	for _, t := range tables {
		if skip[strings.ToLower(t)] {
			continue
		}
		tableName := t
		// Oracle 默认返回大写表名，统一转小写存储，保持与现有配置（小写）一致
		if r.db.Dialector.Name() == "oracle" {
			tableName = strings.ToLower(t)
		}
		cols, err := r.db.Migrator().ColumnTypes(tableName)
		if err != nil {
			return nil, err
		}
		var fields []IntrospectedField
		for _, c := range cols {
			dt := strings.ToLower(c.DatabaseTypeName())
			pk, _ := c.PrimaryKey()
			fields = append(fields, IntrospectedField{
				TableName:    tableName,
				ColumnName:   c.Name(),
				DataType:     dt,
				IsPrimaryKey: pk,
			})
		}
		out[tableName] = fields
	}
	return out, nil
}

// UpsertTableConfig 按表名 upsert。若已存在则保留用户已填的 title/comment/enabled，
// 仅当这些字段为空时才用导入值补齐（导入时这些业务字段通常为空）。
func (r *PermissionRepo) UpsertTableConfig(m *model.TableConfig) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"updated_at"}),
	}).Create(m).Error
}

// UpsertFieldConfig 按 (table_name, name) upsert。
// 仅当库中该字段尚无 title/comment 时才用导入值补齐，避免覆盖用户手动调整的注释。
// data_type 每次都刷新（结构信息以真实库为准）。
func (r *PermissionRepo) UpsertFieldConfig(m *model.FieldConfig) error {
	var exist model.FieldConfig
	err := r.db.Where("table_name = ? AND name = ?", m.TableName, m.Name).First(&exist).Error
	if err == gorm.ErrRecordNotFound {
		return r.db.Create(m).Error
	}
	if err != nil {
		return err
	}
	// 已存在：刷新类型，保留用户已填的业务字段
	updates := map[string]interface{}{
		"data_type": m.DataType,
	}
	if exist.Title == "" && m.Title != "" {
		updates["title"] = m.Title
	}
	if exist.Comment == "" && m.Comment != "" {
		updates["comment"] = m.Comment
	}
	if !exist.Sensitive && m.Sensitive {
		updates["sensitive"] = m.Sensitive
	}
	updates["updated_at"] = time.Now()
	return r.db.Model(&exist).Updates(updates).Error
}

// CountConfigs 返回当前已配置的表/字段数量，便于导入后反馈。
func (r *PermissionRepo) CountConfigs() (tables, fields int64, err error) {
	if e := r.db.Model(&model.TableConfig{}).Count(&tables).Error; e != nil {
		return 0, 0, e
	}
	if e := r.db.Model(&model.FieldConfig{}).Count(&fields).Error; e != nil {
		return 0, 0, e
	}
	return tables, fields, nil
}
