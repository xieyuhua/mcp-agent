package repository

import (
	"gorm.io/gorm"

	"company.com/mcp-data-server/internal/model"
)

// PermissionRepo 权限策略与脱敏规则的持久化层。
type PermissionRepo struct {
	db *gorm.DB
}

func NewPermissionRepo(db *gorm.DB) *PermissionRepo {
	return &PermissionRepo{db: db}
}

// DB 返回底层数据库连接（用于测试/审计服务构造）。
func (r *PermissionRepo) DB() *gorm.DB { return r.db }

// --- PermissionPolicy ---

// GetPolicy 优先取租户级策略，否则取平台全局默认（TenantID=""）。
func (r *PermissionRepo) GetPolicy(tenantID, role string) (*model.PermissionPolicy, error) {
	var p model.PermissionPolicy
	err := r.db.Where("tenant_id = ? AND role = ?", tenantID, role).First(&p).Error
	if err == nil {
		return &p, nil
	}
	if err == gorm.ErrRecordNotFound {
		err = r.db.Where("tenant_id = ? AND role = ?", "", role).First(&p).Error
		if err == nil {
			return &p, nil
		}
	}
	return nil, err
}

// ListPolicies 列出某租户（含平台默认）的全部策略。
func (r *PermissionRepo) ListPolicies(tenantID string) ([]model.PermissionPolicy, error) {
	var list []model.PermissionPolicy
	err := r.db.Where("tenant_id = ? OR tenant_id = ?", tenantID, "").
		Order("tenant_id ASC, role").Find(&list).Error
	return list, err
}

// UpsertPolicy 新增或更新策略（按 tenant+role 唯一）。
func (r *PermissionRepo) UpsertPolicy(p *model.PermissionPolicy) error {
	var existing model.PermissionPolicy
	err := r.db.Where("tenant_id = ? AND role = ?", p.TenantID, p.Role).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return r.db.Create(p).Error
	}
	if err != nil {
		return err
	}
	p.ID = existing.ID
	return r.db.Model(&existing).Select("data_scope", "allowed_tables", "can_raw_sql", "updated_by").Updates(map[string]interface{}{
		"data_scope":     p.DataScope,
		"allowed_tables": p.AllowedTables,
		"can_raw_sql":    p.CanRawSQL,
		"updated_by":     p.UpdatedBy,
	}).Error
}

// DeletePolicy 删除租户级策略（回退到平台默认）。
func (r *PermissionRepo) DeletePolicy(tenantID, role string) error {
	return r.db.Where("tenant_id = ? AND role = ?", tenantID, role).Delete(&model.PermissionPolicy{}).Error
}

// --- FieldPermission ---

// ListFieldPermissions 列出某租户（含平台默认）的全部字段权限。
func (r *PermissionRepo) ListFieldPermissions(tenantID string) ([]model.FieldPermission, error) {
	var list []model.FieldPermission
	err := r.db.Where("tenant_id = ? OR tenant_id = ?", tenantID, "").
		Order("tenant_id ASC, role, table_name, column").Find(&list).Error
	return list, err
}

// GetFieldPermissionsAsMap 合并平台默认与租户级字段权限为 table->column->hidden（租户级覆盖平台级）。
func (r *PermissionRepo) GetFieldPermissionsAsMap(tenantID, role string) (map[string]map[string]bool, error) {
	var list []model.FieldPermission
	err := r.db.Where("(tenant_id = ? OR tenant_id = ?) AND role = ?", tenantID, "", role).
		Order("tenant_id ASC").Find(&list).Error
	if err != nil {
		return nil, err
	}
	return model.ParseHiddenFieldsMap(list), nil
}

// UpsertFieldPermission 新增或更新字段权限（按 tenant+role+table+column 唯一）。
func (r *PermissionRepo) UpsertFieldPermission(fp *model.FieldPermission) error {
	var existing model.FieldPermission
	err := r.db.Where("tenant_id = ? AND role = ? AND table_name = ? AND column = ?",
		fp.TenantID, fp.Role, fp.TableName, fp.Column).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return r.db.Create(fp).Error
	}
	if err != nil {
		return err
	}
	fp.ID = existing.ID
	return r.db.Model(&existing).Select("hidden", "updated_by").Updates(map[string]interface{}{
		"hidden":     fp.Hidden,
		"updated_by": fp.UpdatedBy,
	}).Error
}

// DeleteFieldPermission 删除租户级字段权限。
func (r *PermissionRepo) DeleteFieldPermission(tenantID, role, table, column string) error {
	return r.db.Where("tenant_id = ? AND role = ? AND table_name = ? AND column = ?",
		tenantID, role, table, column).Delete(&model.FieldPermission{}).Error
}

// DeleteFieldPermissionByRole 删除某租户+角色下的全部字段权限（用于策略级联清理）。
func (r *PermissionRepo) DeleteFieldPermissionByRole(tenantID, role string) error {
	return r.db.Where("tenant_id = ? AND role = ?", tenantID, role).Delete(&model.FieldPermission{}).Error
}

// --- MaskRule ---

// ListMaskRules 列出某租户（含平台默认）的全部脱敏规则。
func (r *PermissionRepo) ListMaskRules(tenantID string) ([]model.MaskRule, error) {
	var list []model.MaskRule
	err := r.db.Where("tenant_id = ? OR tenant_id = ?", tenantID, "").
		Order("tenant_id ASC, table_name, column").Find(&list).Error
	return list, err
}

// GetMaskRulesAsMap 合并平台默认与租户级规则为表->列->类型（租户级覆盖平台级）。
func (r *PermissionRepo) GetMaskRulesAsMap(tenantID string) (map[string]map[string]model.MaskRule, error) {
	rules, err := r.ListMaskRules(tenantID)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]model.MaskRule{}
	for _, rule := range rules {
		if out[rule.TableName] == nil {
			out[rule.TableName] = map[string]model.MaskRule{}
		}
		// 平台默认先写入，租户级后写入即覆盖（含 disabled，用于显式关闭）
		out[rule.TableName][rule.Column] = rule
	}
	return out, nil
}

// UpsertMaskRule 新增或更新脱敏规则（按 tenant+table+column 唯一）。
func (r *PermissionRepo) UpsertMaskRule(rule *model.MaskRule) error {
	var existing model.MaskRule
	err := r.db.Where("tenant_id = ? AND table_name = ? AND column = ?", rule.TenantID, rule.TableName, rule.Column).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return r.db.Create(rule).Error
	}
	if err != nil {
		return err
	}
	rule.ID = existing.ID
	return r.db.Model(&existing).Select("mask_type", "enabled", "updated_by").Updates(map[string]interface{}{
		"mask_type":  rule.MaskType,
		"enabled":    rule.Enabled,
		"updated_by": rule.UpdatedBy,
	}).Error
}

// DeleteMaskRule 删除租户级脱敏规则。
func (r *PermissionRepo) DeleteMaskRule(tenantID, table, column string) error {
	return r.db.Where("tenant_id = ? AND table_name = ? AND column = ?", tenantID, table, column).
		Delete(&model.MaskRule{}).Error
}
