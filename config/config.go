package config

import (
	"encoding/json"
	"os"
)

// Config 服务运行配置。
type Config struct {
	DBDialect string `json:"db_dialect"` // mysql | sqlite | oracle（查询的目标业务库）
	DBDSN     string `json:"db_dsn"`     // 连接串。mysql: user:pass@tcp(host:3306)/dbname；sqlite: ./data.db；oracle: oracle://user:pass@host:1521/service
	SeedDemo  bool   `json:"seed_demo"`  // 是否写入演示数据

	Transport string `json:"transport"`  // stdio | http | both
	HTTPAddr  string `json:"http_addr"`  // HTTP 监听地址，如 :8081
	WorkDir   string `json:"work_dir"`   // 文件工具沙箱根目录
	SandboxEnabled bool `json:"sandbox_enabled"` // 是否启用沙箱

	SearchProvider string `json:"search_provider"` // duckduckgo | bing | auto

	SessionSecret string `json:"session_secret"` // 后台会话签名密钥，建议通过环境变量注入

	// LLM AI 一键完善（业务名称/表注释）所用的大模型配置（OpenAI 兼容 chat 接口）。
	LLM LLMConfig `json:"llm"`
}

// LLMConfig 大模型连接配置（兼容 Ollama / OpenAI 风格 chat 接口），用于后台「AI 一键完善」。
type LLMConfig struct {
	Provider    string  `json:"provider"`     // ollama | openai（两者均使用 chat/completions 风格接口）
	BaseURL     string  `json:"base_url"`     // 服务地址，如 http://localhost:11434
	APIKey      string  `json:"api_key"`      // 仅 openai 兼容服务需要
	Model       string  `json:"model"`        // 模型名，如 qwen2.5:14b
	Temperature float64 `json:"temperature"`  // 生成温度
	MaxTokens   int     `json:"max_tokens"`    // 单次生成上限
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
	if v := os.Getenv("SESSION_SECRET"); v != "" {
		c.SessionSecret = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		c.LLM.BaseURL = v
	}
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		c.LLM.Model = v
	}
	return c, nil
}
