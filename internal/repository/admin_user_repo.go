package repository

import (
	"company.com/mcp-data-server/internal/model"
)

// GetAdminUser 按用户名查询后台账号（含密码哈希与 salt）。
func (r *PermissionRepo) GetAdminUser(username string) (*model.AdminUser, error) {
	var u model.AdminUser
	if err := r.db.Where("username = ?", username).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// SaveAdminUser 创建或更新后台账号。
func (r *PermissionRepo) SaveAdminUser(u *model.AdminUser) error {
	return r.db.Save(u).Error
}

// CountAdminUsers 统计后台账号总数，用于判断是否为首次安装。
func (r *PermissionRepo) CountAdminUsers() (int64, error) {
	var cnt int64
	if err := r.db.Model(&model.AdminUser{}).Count(&cnt).Error; err != nil {
		return 0, err
	}
	return cnt, nil
}
