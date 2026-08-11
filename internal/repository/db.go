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

	// Oracle 使用纯 Go 驱动（基于 sijms/go-ora），无需 CGO / Oracle 客户端。
	oracleDriver "github.com/godoes/gorm-oracle"
)

// OpenDB 按配置打开目标数据库（mysql / sqlite / oracle），并设置合理的连接池参数。
// 该库既是 agent 查询的业务目标库，也承载后台权限元数据（同一连接）。
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
	case "oracle":
		dsn := c.DBDSN
		if dsn == "" {
			return nil, fmt.Errorf("oracle 连接需要配置 db_dsn，格式如 oracle://user:pass@host:1521/service")
		}
		dialector = oracleDriver.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported db_dialect: %s（支持 mysql / sqlite / oracle）", c.DBDialect)
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
	case "oracle":
		// Oracle 服务端连接数有限，控制池大小避免耗尽；复用空闲连接。
		sqlDB.SetMaxOpenConns(20)
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetConnMaxLifetime(1 * time.Hour)
	}
	return nil
}

// AutoMigrate 自动建表。
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.Tenant{},
		&model.Customer{},
		&model.Order{},
		&model.AuditLog{},
		// 权限与元数据配置
		&model.Role{},
		&model.TableConfig{},
		&model.FieldConfig{},
		&model.TableRelation{},
		&model.RolePolicy{},
		&model.RoleFieldGrant{},
		&model.AdminUser{},
	)
}
