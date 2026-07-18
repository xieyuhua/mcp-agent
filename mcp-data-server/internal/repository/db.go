package repository

import (
	"fmt"
	"log"
	"time"

	"company.com/mcp-data-server/config"
	"company.com/mcp-data-server/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// OpenDB 按配置打开数据库（mysql / sqlite），并设置合理的连接池参数。
func OpenDB(c *config.Config) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch c.DBDialect {
	case "mysql":
		dialector = mysql.Open(c.DBDSN)
	case "sqlite":
		// _pragma 参数在连接串里设置 WAL 和 busy_timeout，避免并发写冲突。
		dsn := c.DBDSN
		if dsn == "" {
			dsn = "./data.db"
		}
		dialector = sqlite.Open(dsn + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	default:
		return nil, fmt.Errorf("unsupported db_dialect: %s", c.DBDialect)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := configurePool(db, c.DBDialect); err != nil {
		return nil, fmt.Errorf("configure pool: %w", err)
	}
	return db, nil
}

// configurePool 根据数据库类型设置连接池与并发参数。
func configurePool(db *gorm.DB, dialect string) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	switch dialect {
	case "mysql":
		// MySQL 默认不限制连接数，高并发容易打爆服务端；设置上限并复用空闲连接。
		sqlDB.SetMaxOpenConns(25)
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetConnMaxLifetime(1 * time.Hour)
	case "sqlite":
		// SQLite 写是串行的，WAL 模式下可并发读。控制连接数避免过多写竞争。
		sqlDB.SetMaxOpenConns(10)
		sqlDB.SetMaxIdleConns(5)
		sqlDB.SetConnMaxLifetime(1 * time.Hour)
		// 再执行一次 PRAGMA 兜底，确保 WAL 生效。
		if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
			log.Printf("warn: sqlite WAL mode: %v", err)
		}
		if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil {
			log.Printf("warn: sqlite busy_timeout: %v", err)
		}
	}
	return nil
}

// AutoMigrate 自动建表。
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.Tenant{},
		&model.User{},
		&model.Customer{},
		&model.Order{},
		&model.AuditLog{},
		&model.Role{},
		&model.PermissionPolicy{},
		&model.MaskRule{},
		&model.FieldPermission{},
	)
}
