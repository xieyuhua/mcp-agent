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

	// WorkDir 文件/目录读写工具的根目录（沙箱）。为空时默认进程工作目录。
	// 所有文件工具都只能在该目录及其子目录内操作，禁止越界访问（当 SandboxEnabled=true 时）。
	WorkDir string `json:"work_dir"`

	// SandboxEnabled 是否启用工作目录沙箱（默认 true）。
	// true：所有文件工具只能访问 WorkDir 及其子目录，拦截 ../ 越界。
	// false：进入“系统环境”模式，允许访问任意绝对路径（相对路径相对进程工作目录），
	// 适用于受信任的内网部署，但请谨慎——文件工具将能读写服务器上的任意文件。
	SandboxEnabled bool `json:"sandbox_enabled"`

	// SearchProvider 联网搜索提供商：duckduckgo（默认）| bing | auto（优先 DuckDuckGo，失败回退 Bing）。
	SearchProvider string `json:"search_provider"`
}

// Load 加载配置：先读文件，再用环境变量覆盖。
func Load(path string) (*Config, error) {
	c := &Config{
		DBDialect:      "sqlite",
		DBDSN:          "./data.db",
		JWTSecret:      "change-me-in-production",
		MaskEnabled:    true,
		SeedDemo:       true,
		SandboxEnabled: true, // 默认启用沙箱，文件工具只能访问 WorkDir 内
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
