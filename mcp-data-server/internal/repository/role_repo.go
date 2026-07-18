package repository

import (
	"time"

	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/model"

	"gorm.io/gorm"
)

// RoleRepo 角色定义持久化层。
type RoleRepo struct {
	db *gorm.DB
}

func NewRoleRepo(db *gorm.DB) *RoleRepo {
	return &RoleRepo{db: db}
}

// SeedBuiltinRoles 在数据库为空时写入内置角色，确保旧数据平滑升级。
func (r *RoleRepo) SeedBuiltinRoles(updatedBy string) error {
	var cnt int64
	if err := r.db.Model(&model.Role{}).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return nil
	}
	now := time.Now()
	roles := []model.Role{
		{TenantID: "", Name: auth.RoleSuperAdmin, DisplayName: "平台运营", Description: "跨租户全部数据权限", IsBuiltin: true, UpdatedBy: updatedBy, UpdatedAt: now},
		{TenantID: "", Name: auth.RoleRegionMgr, DisplayName: "区域负责人", Description: "本租户+本区域数据", IsBuiltin: true, UpdatedBy: updatedBy, UpdatedAt: now},
		{TenantID: "", Name: auth.RoleStoreMgr, DisplayName: "门店店长", Description: "本门店数据", IsBuiltin: true, UpdatedBy: updatedBy, UpdatedAt: now},
		{TenantID: "", Name: auth.RoleStaff, DisplayName: "普通岗位", Description: "本门店数据", IsBuiltin: true, UpdatedBy: updatedBy, UpdatedAt: now},
		{TenantID: "", Name: auth.RoleAnalyst, DisplayName: "数据分析师", Description: "本租户只读数据", IsBuiltin: true, UpdatedBy: updatedBy, UpdatedAt: now},
	}
	return r.db.Create(&roles).Error
}

// ListRoles 列出某租户（含平台全局）的全部角色。
func (r *RoleRepo) ListRoles(tenantID string) ([]model.Role, error) {
	var list []model.Role
	err := r.db.Where("tenant_id = ? OR tenant_id = ?", tenantID, "").
		Order("is_builtin DESC, name").Find(&list).Error
	return list, err
}

// GetRoleByName 按租户+名称取角色。
func (r *RoleRepo) GetRoleByName(tenantID, name string) (*model.Role, error) {
	var role model.Role
	err := r.db.Where("tenant_id = ? AND name = ?", tenantID, name).First(&role).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &role, nil
}

// UpsertRole 新增或更新角色（tenant+name 唯一）。
func (r *RoleRepo) UpsertRole(role *model.Role) error {
	var existing model.Role
	err := r.db.Where("tenant_id = ? AND name = ?", role.TenantID, role.Name).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return r.db.Create(role).Error
	}
	if err != nil {
		return err
	}
	role.ID = existing.ID
	role.IsBuiltin = existing.IsBuiltin // 内置标志不可被修改
	return r.db.Model(&existing).Select("display_name", "description", "updated_by", "updated_at").Updates(map[string]interface{}{
		"display_name": role.DisplayName,
		"description":  role.Description,
		"updated_by":   role.UpdatedBy,
		"updated_at":   role.UpdatedAt,
	}).Error
}

// DeleteRole 删除非内置角色。
func (r *RoleRepo) DeleteRole(tenantID, name string) error {
	return r.db.Where("tenant_id = ? AND name = ? AND is_builtin = ?", tenantID, name, false).
		Delete(&model.Role{}).Error
}

// IsBuiltin 判断某角色是否为内置角色。
func (r *RoleRepo) IsBuiltin(tenantID, name string) (bool, error) {
	var role model.Role
	err := r.db.Where("tenant_id = ? AND name = ?", tenantID, name).First(&role).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return role.IsBuiltin, nil
}
