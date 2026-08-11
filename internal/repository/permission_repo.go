package repository

import (
	"company.com/mcp-data-server/internal/model"

	"gorm.io/gorm"
)

// PermissionRepo 权限与元数据配置的数据访问层。
type PermissionRepo struct {
	db *gorm.DB
}

func NewPermissionRepo(db *gorm.DB) *PermissionRepo { return &PermissionRepo{db: db} }

func (r *PermissionRepo) DB() *gorm.DB { return r.db }

// ---- Role ----

func (r *PermissionRepo) ListRoles() ([]model.Role, error) {
	var out []model.Role
	err := r.db.Order("id asc").Find(&out).Error
	return out, err
}

func (r *PermissionRepo) SaveRole(m *model.Role) error { return r.db.Save(m).Error }

func (r *PermissionRepo) DeleteRole(id uint) error {
	return r.db.Delete(&model.Role{}, id).Error
}

// ---- TableConfig ----

func (r *PermissionRepo) ListTables() ([]model.TableConfig, error) {
	var out []model.TableConfig
	err := r.db.Order("id asc").Find(&out).Error
	return out, err
}

func (r *PermissionRepo) GetTable(id uint) (*model.TableConfig, error) {
	var m model.TableConfig
	err := r.db.First(&m, id).Error
	return &m, err
}

func (r *PermissionRepo) ListEnabledTables() ([]model.TableConfig, error) {
	var out []model.TableConfig
	err := r.db.Where("enabled = ?", true).Order("id asc").Find(&out).Error
	return out, err
}

func (r *PermissionRepo) SaveTable(m *model.TableConfig) error { return r.db.Save(m).Error }

func (r *PermissionRepo) DeleteTable(id uint) error {
	return r.db.Delete(&model.TableConfig{}, id).Error
}

// ---- FieldConfig ----

func (r *PermissionRepo) ListFields(table string) ([]model.FieldConfig, error) {
	var out []model.FieldConfig
	q := r.db.Order("id asc")
	if table != "" {
		q = q.Where("table_name = ?", table)
	}
	err := q.Find(&out).Error
	return out, err
}

func (r *PermissionRepo) SaveField(m *model.FieldConfig) error { return r.db.Save(m).Error }

func (r *PermissionRepo) DeleteField(id uint) error {
	return r.db.Delete(&model.FieldConfig{}, id).Error
}

// ---- TableRelation ----

func (r *PermissionRepo) ListRelations() ([]model.TableRelation, error) {
	var out []model.TableRelation
	err := r.db.Order("id asc").Find(&out).Error
	return out, err
}

func (r *PermissionRepo) SaveRelation(m *model.TableRelation) error { return r.db.Save(m).Error }

func (r *PermissionRepo) DeleteRelation(id uint) error {
	return r.db.Delete(&model.TableRelation{}, id).Error
}

// ---- RolePolicy ----

func (r *PermissionRepo) ListPolicies(role string) ([]model.RolePolicy, error) {
	var out []model.RolePolicy
	q := r.db.Order("id asc")
	if role != "" {
		q = q.Where("role = ?", role)
	}
	err := q.Find(&out).Error
	return out, err
}

// EnabledPoliciesByRole 返回某角色启用中的行级策略。
func (r *PermissionRepo) EnabledPoliciesByRole(role string) ([]model.RolePolicy, error) {
	var out []model.RolePolicy
	err := r.db.Where("role = ? AND enabled = ?", role, true).Find(&out).Error
	return out, err
}

func (r *PermissionRepo) SavePolicy(m *model.RolePolicy) error { return r.db.Save(m).Error }

func (r *PermissionRepo) DeletePolicy(id uint) error {
	return r.db.Delete(&model.RolePolicy{}, id).Error
}

// ---- RoleFieldGrant ----

func (r *PermissionRepo) ListFieldGrants(role, table string) ([]model.RoleFieldGrant, error) {
	var out []model.RoleFieldGrant
	q := r.db.Order("id asc")
	if role != "" {
		q = q.Where("role = ?", role)
	}
	if table != "" {
		q = q.Where("table_name = ?", table)
	}
	err := q.Find(&out).Error
	return out, err
}

func (r *PermissionRepo) SaveFieldGrant(m *model.RoleFieldGrant) error { return r.db.Save(m).Error }

func (r *PermissionRepo) DeleteFieldGrant(id uint) error {
	return r.db.Delete(&model.RoleFieldGrant{}, id).Error
}
