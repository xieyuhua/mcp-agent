package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config 数据分析助手运行配置。
// 该结构同时用于：文件加载（config.json 作为初始种子）、数据库配置（运行时以 DB 为准）、
// 后台管理页面展示与回写。
type Config struct {
	// LLM 本地大模型配置（兼容 Ollama / OpenAI 风格 chat 接口）。
	LLM LLMConfig `json:"llm"`

	// MCP 后端：内置 mcp-data-server（stdio 子进程）或远程 MCP 服务（HTTP）。
	MCP MCPConfig `json:"mcp"`

	// Agent 行为参数。
	Agent AgentConfig `json:"agent"`

	// Prompts 系统提示词配置（可在后台编辑，存数据库，避免写死）。
	Prompts PromptsConfig `json:"prompts"`
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

// PromptsConfig 系统提示词配置（可在后台编辑，持久化到数据库）。
type PromptsConfig struct {
	// Builtin 内置 mcp-data-server（数据库分析场景）的系统提示词。
	Builtin string `json:"builtin"`
	// Remote 对接通用远程 MCP 服务时的系统提示词。
	Remote string `json:"remote"`
}

// MCPConfig 后端 MCP 服务配置，支持两种对接方案：
//   - mode="local"（默认）：拉起内置 mcp-data-server 子进程（stdio）
//   - mode="remote"：对接远程 MCP 服务（streamable-http / sse）
type MCPConfig struct {
	// Mode 对接方案："local" | "remote"，默认 local。
	Mode string `json:"mode"`

	// --- local（内置 mcp-data-server 子进程）相关 ---
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
	// 是否启用脱敏（透传给 mcp-data-server 的 MASK_ENABLED）。
	MaskEnabled bool `json:"mask_enabled"`
	// 是否写入演示数据（透传给 mcp-data-server 的 SEED_DEMO）。
	SeedDemo bool `json:"seed_demo"`

	// --- remote（远程 MCP 服务）相关 ---
	// BaseURL 远程地址，如 http://192.168.1.10:9000/mcp 或 .../sse
	BaseURL string `json:"base_url"`
	// Transport 远程传输方式："streamable-http"（默认）| "sse"（旧版）。
	Transport string `json:"transport"`
	// APIKey 远程服务鉴权（放入 Authorization: Bearer）。
	APIKey string `json:"api_key"`
	// Headers 远程请求额外头（如自定义鉴权）。
	Headers map[string]string `json:"headers"`

	// 登录凭据：本地模式以该账号登录 mcp-data-server 获取 token；
	// 远程模式若远程服务也需要登录，可在此提供（由对应实现使用）。
	Username string `json:"username"`
	Password string `json:"password"`
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
	c := DefaultConfig()
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
	if c.Prompts.Builtin == "" {
		c.Prompts.Builtin = DefaultBuiltinPrompt
	}
	if c.Prompts.Remote == "" {
		c.Prompts.Remote = DefaultRemotePrompt
	}
	return c, nil
}

// DefaultBuiltinPrompt 内置 mcp-data-server（数据库分析）场景的默认系统提示词。
// 运行时可在后台覆盖（存数据库），此处仅作为初始种子/兜底。
const DefaultBuiltinPrompt = `你是一个企业数据分析助手。你的工作流程是：
1. 理解用户用自然语言提出的分析问题；
2. 必要时用 describe_table 了解表结构，用 run_sql（平台运营）或 query_data（其他角色）生成并执行 SQL；
3. 拿到查询结果后，用数据给出清晰、有洞察的分析结论（中文），并给出关键数字。

4. 当结果适合可视化（分组对比、占比、趋势）时，调用 render_chart 生成图表，再给出文字结论。

重要约束：
- 你除了能做数据库数据分析，还可以用 query_weather 联网查询任意城市实时天气；
- 只能进行只读分析，禁止任何写操作/删除/DDL；
- 权限隔离、数据脱敏、危险 SQL 拦截都由后端 MCP 服务统一处理，你只需专注生成正确的分析 SQL；
- 如果工具返回权限不足或报错，请如实告知用户原因，不要编造数据；
- render_chart 的 categories 与每个 series.data 必须长度一致、顺序对应，数值取自真实查询结果；
- 最终回答要面向业务，给出结论、数据支撑与建议。`

// DefaultRemotePrompt 对接通用远程 MCP 服务时的默认系统提示词。
const DefaultRemotePrompt = `你是一个智能助手，可以调用多种工具来完成用户任务。
可用工具包括：
- 内置工具 query_weather（联网查询任意城市实时天气）；
- 内置工具 render_chart（把分析结果生成图表供前端展示）；
- 远程 MCP 服务提供的工具（见下方工具列表，名称与参数以工具定义为准）。

工作准则：
- 根据用户的自然语言问题，自行决定调用哪些工具，必要时可多次调用并组合结果；
- 拿到工具返回后，用中文给出清晰、有依据的结论，并引用关键数据；
- 如果工具返回错误或权限不足，请如实告知用户原因，不要编造数据；
- render_chart 的 categories 与每个 series.data 必须长度一致、顺序对应，数值取自真实查询结果；
- 最终回答要面向用户意图，给出结论、数据支撑与建议。`

// DefaultConfig 返回带完整默认值的配置（不读取任何文件）。
func DefaultConfig() *Config {
	return &Config{
		LLM: LLMConfig{
			Provider:    "ollama",
			BaseURL:     "http://localhost:11434",
			Model:       "qwen2.5:14b",
			Temperature: 0.2,
			MaxTokens:   2048,
		},
		MCP: MCPConfig{
			Mode:        "local",
			ServerPath:  "../mcp-data-server/main.exe",
			Username:    "admin",
			Password:    "admin123",
			MaskEnabled: true,
			SeedDemo:   true,
		},
		Agent: AgentConfig{
			MaxSteps:        8,
			UseNativeTools:  false,
			MaxResultRows:   200,
		},
		Prompts: PromptsConfig{
			Builtin: DefaultBuiltinPrompt,
			Remote:  DefaultRemotePrompt,
		},
	}
}
