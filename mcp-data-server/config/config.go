package config

import (
	"encoding/json"
	"os"
)

// Config 服务运行配置，支持文件 + 环境变量覆盖。
type Config struct {
	DBDialect   string `json:"db_dialect"`   // mysql | sqlite
	DBDSN       string `json:"db_dsn"`       // mysql: user:pass@tcp(host:3306)/db?charset=utf8mb4&parseTime=true  sqlite: ./data.db
	JWTSecret   string `json:"jwt_secret"`   // 令牌签名密钥
	MaskEnabled bool   `json:"mask_enabled"` // 是否开启数据脱敏
	SeedDemo    bool   `json:"seed_demo"`    // 是否写入演示数据
}

// Load 加载配置：先读文件，再用环境变量覆盖。
func Load(path string) (*Config, error) {
	c := &Config{
		DBDialect:   "sqlite",
		DBDSN:       "./data.db",
		JWTSecret:   "change-me-in-production",
		MaskEnabled: true,
		SeedDemo:    true,
	}
	if path == "" {
		path = os.Getenv("CONFIG_FILE")
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	}
	if v := os.Getenv("DB_DIALECT"); v != "" {
		c.DBDialect = v
	}
	if v := os.Getenv("DB_DSN"); v != "" {
		c.DBDSN = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		c.JWTSecret = v
	}
	return c, nil
}
