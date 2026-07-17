package repository

import (
	"fmt"

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// OpenDB 按配置打开数据库（mysql / sqlite）。
func OpenDB(c *config.Config) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch c.DBDialect {
	case "mysql":
		dialector = mysql.Open(c.DBDSN)
	case "sqlite":
		dialector = sqlite.Open(c.DBDSN)
	default:
		return nil, fmt.Errorf("unsupported db_dialect: %s", c.DBDialect)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	return db, nil
}

// AutoMigrate 自动建表。
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.Tenant{},
		&model.User{},
		&model.Customer{},
		&model.Order{},
		&model.AuditLog{},
		&model.PermissionPolicy{},
		&model.MaskRule{},
	)
}
