package config

import (
	"encoding/json"
	"os"
)

// Config 服务运行配置。
type Config struct {
	DBDialect string `json:"db_dialect"` // mysql | sqlite
	DBDSN     string `json:"db_dsn"`     // 连接串
	SeedDemo  bool   `json:"seed_demo"`  // 是否写入演示数据

	Transport string `json:"transport"`  // stdio | http | both
	HTTPAddr  string `json:"http_addr"`  // HTTP 监听地址，如 :8081
	WorkDir   string `json:"work_dir"`   // 文件工具沙箱根目录
	SandboxEnabled bool `json:"sandbox_enabled"` // 是否启用沙箱

	SearchProvider string `json:"search_provider"` // duckduckgo | bing | auto
}

// Load 加载配置：先读文件，再用环境变量覆盖。
func Load(path string) (*Config, error) {
	c := &Config{
		DBDialect:      "sqlite",
		DBDSN:          "./data.db",
		SeedDemo:       true,
		SandboxEnabled: true,
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
	if v := os.Getenv("SEED_DEMO"); v != "" {
		c.SeedDemo = v == "true" || v == "1" || v == "yes"
	}
	if v := os.Getenv("TRANSPORT"); v != "" {
		c.Transport = v
	}
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		c.HTTPAddr = v
	}
	if v := os.Getenv("WORK_DIR"); v != "" {
		c.WorkDir = v
	}
	if v := os.Getenv("SANDBOX_ENABLED"); v != "" {
		c.SandboxEnabled = v == "true" || v == "1" || v == "yes"
	}
	if v := os.Getenv("SEARCH_PROVIDER"); v != "" {
		c.SearchProvider = v
	}
	return c, nil
}
