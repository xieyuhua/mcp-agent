package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config 数据分析助手运行配置。
type Config struct {
	// LLM 本地大模型配置（兼容 Ollama / OpenAI 风格 chat 接口）。
	LLM LLMConfig `json:"llm"`

	// MCP 后端：即前面开发的 mcp-data-server（stdio 子进程）。
	MCP MCPConfig `json:"mcp"`

	// Agent 行为参数。
	Agent AgentConfig `json:"agent"`
}

// LLMConfig 本地大模型连接配置。
type LLMConfig struct {
	// Provider: ollama | openai。两者均使用 chat/completions 风格接口。
	Provider string `json:"provider"`
	// BaseURL 本地大模型服务地址，例如 http://localhost:11434 （Ollama）。
	BaseURL string `json:"base_url"`
	// Model 模型名，例如 qwen2.5:14b / llama3.1:8b。
	Model string `json:"model"`
	// APIKey 仅 openai 兼容服务需要，本地部署一般留空。
	APIKey string `json:"api_key"`
	// 生成温度。
	Temperature float64 `json:"temperature"`
	// MaxTokens 单次生成上限。
	MaxTokens int `json:"max_tokens"`
}

// MCPConfig 后端 MCP 服务（mcp-data-server）配置。
type MCPConfig struct {
	// ServerPath 编译好的 mcp-data-server 可执行文件路径。
	ServerPath string `json:"server_path"`
	// DBDialect 后端数据库类型：sqlite（默认，演示）| mysql（真实数据分析）。
	DBDialect string `json:"db_dialect"`
	// DBDsn 数据库连接串。
	//   sqlite: ./data.db
	//   mysql:  user:pass@tcp(127.0.0.1:3306)/dbname?charset=utf8mb4&parseTime=true
	DBDsn string `json:"db_dsn"`
	// 传给子进程的额外环境变量（覆盖 db_dialect/db_dsn 等）。
	Env map[string]string `json:"env"`
	// 登录凭据：Agent 启动后以该账号登录，获取 token 用于后续工具调用。
	Username string `json:"username"`
	Password string `json:"password"`
	// 是否启用脱敏（透传给 mcp-data-server 的 MASK_ENABLED）。
	MaskEnabled bool `json:"mask_enabled"`
	// 是否写入演示数据（透传给 mcp-data-server 的 SEED_DEMO）。
	SeedDemo bool `json:"seed_demo"`
}

// AgentConfig Agent 编排参数。
type AgentConfig struct {
	// MaxSteps ReAct 循环最大步数，防止死循环。
	MaxSteps int `json:"max_steps"`
	// 是否使用结构化（原生）工具调用；false 时退化为提示词约束的 JSON 工具调用。
	UseNativeTools bool `json:"use_native_tools"`
	// 单次工具返回结果最多保留多少行，避免上下文爆炸。
	MaxResultRows int `json:"max_result_rows"`
}

// Load 加载配置：文件 + 环境变量覆盖（CONFIG_FILE）。
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("CONFIG_FILE")
	}
	c := defaultConfig()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := json.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if c.LLM.Provider == "" {
		c.LLM.Provider = "ollama"
	}
	if c.LLM.BaseURL == "" {
		c.LLM.BaseURL = "http://localhost:11434"
	}
	if c.LLM.Model == "" {
		c.LLM.Model = "qwen2.5:14b"
	}
	if c.LLM.Temperature == 0 {
		c.LLM.Temperature = 0.2
	}
	if c.LLM.MaxTokens == 0 {
		c.LLM.MaxTokens = 2048
	}
	if c.Agent.MaxSteps == 0 {
		c.Agent.MaxSteps = 8
	}
	if c.Agent.MaxResultRows == 0 {
		c.Agent.MaxResultRows = 200
	}
	return c, nil
}

func defaultConfig() *Config {
	return &Config{
		MCP: MCPConfig{
			ServerPath: "../mcp-data-server/main.exe",
			Username:   "admin",
			Password:   "admin123",
			MaskEnabled: true,
			SeedDemo:   true,
		},
	}
}
