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

	// Transport 传输方式：stdio（默认，子进程）| http（仅 HTTP）| both（两者同时）。
	Transport string `json:"transport"`
	// HTTPAddr HTTP 监听地址（transport=http/both 时生效），如 :8081。
	HTTPAddr string `json:"http_addr"`
	// WebDir 外部 Web 资源目录；为空时由二进制内嵌资源提供（可分离部署）。
	WebDir string `json:"web_dir"`
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
	if v := os.Getenv("SEED_DEMO"); v != "" {
		c.SeedDemo = v == "true" || v == "1" || v == "yes"
	}
	if v := os.Getenv("MASK_ENABLED"); v != "" {
		c.MaskEnabled = v == "true" || v == "1" || v == "yes"
	}
	if v := os.Getenv("TRANSPORT"); v != "" {
		c.Transport = v
	}
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		c.HTTPAddr = v
	}
	if v := os.Getenv("WEB_DIR"); v != "" {
		c.WebDir = v
	}
	return c, nil
}
