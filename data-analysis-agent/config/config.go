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

	// Log 运行日志配置（是否把每个环节的请求日志保存到文件）。
	Log LogConfig `json:"log"`

	// Prompts 系统提示词配置（可在后台编辑，存数据库，避免写死）。
	Prompts PromptsConfig `json:"prompts"`

	// UI 前端展示开关（后台可统一配置）。
	UI UIConfig `json:"ui"`
}

// UIConfig 前端展示开关与品牌配置（后台可热更新）。
type UIConfig struct {
	// ShowDuration 是否展示耗时统计。
	ShowDuration bool `json:"show_duration"`
	// ShowSteps 是否展示分析过程（步骤）。
	ShowSteps bool `json:"show_steps"`
	// ShowImages 是否展示图片/图表。
	ShowImages bool `json:"show_images"`
	// Theme 后台管理页面主题：dark | light | auto（默认 dark）。
	Theme string `json:"theme"`
	// AppTitle 应用标题，前后台共用。
	AppTitle string `json:"app_title"`
	// AppSubtitle 应用副标题/描述，前后台共用。
	AppSubtitle string `json:"app_subtitle"`
	// WorkflowSteps 前台顶部流程步骤文案，用 "→" 分隔，例如：自然语言 → LLM → MCP 权限 → SQL → 图表分析。
	WorkflowSteps string `json:"workflow_steps"`
	// AdminPageSize 后台管理页面默认分页大小。
	AdminPageSize int `json:"admin_page_size"`
	// ChatPageSize 前端聊天消息分页默认大小。
	ChatPageSize int `json:"chat_page_size"`
	// PhoneRequired 是否强制注册时填写手机号。
	PhoneRequired bool `json:"phone_required"`
	// PhoneVerifyRequired 是否强制手机号验证（当前为格式校验，后续可接入短信）。
	PhoneVerifyRequired bool `json:"phone_verify_required"`
}

// LogConfig 运行日志配置。
type LogConfig struct {
	// SaveToFile 是否把每个请求/环节的日志保存到文件（带时间戳）。
	// 开启后日志同时写控制台与 ./logs/agent-YYYY-MM-DD.log；关闭则仅写控制台。
	SaveToFile bool `json:"save_to_file"`
	// Dir 日志文件目录，留空默认 ./logs。
	Dir string `json:"dir"`
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

// MCPConfig 后端 MCP 服务配置。本地 MCP（内置 mcp-data-server 子进程）与
// 远程 MCP（HTTP 服务，如 llama.cpp）为两套相互独立的对接，可各自独立开关：
//   - LocalEnabled：是否启用本地内置 mcp-data-server（stdio 子进程）
//   - RemoteEnabled：是否启用远程 MCP 服务（streamable-http / sse）
// 两者可同时开启（本地作为主 MCP，远程作为额外 MCP 聚合）；也可只开其一。
// Mode 字段仅用于兼容旧配置：当 LocalEnabled/RemoteEnabled 均为 false 时，
// 按 Mode 决定（local=仅本地，remote=仅远程）。
type MCPConfig struct {
	// Mode 兼容旧配置：local | remote（新配置请用下方两个开关）。
	Mode string `json:"mode"`
	// LocalEnabled 是否启用本地内置 mcp-data-server（默认 true）。
	LocalEnabled bool `json:"local_enabled"`
	// RemoteEnabled 是否启用远程 MCP 服务（默认 false，需配置 base_url）。
	RemoteEnabled bool `json:"remote_enabled"`

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
	// WorkDir 文件/目录读写工具的根目录，透传给 mcp-data-server 的 WORK_DIR。
	// 留空时由 mcp-data-server 决定（默认进程工作目录）。
	WorkDir string `json:"work_dir"`
	// SandboxEnabled 是否启用工作目录沙箱（默认 true），透传给 mcp-data-server 的 SANDBOX_ENABLED。
	// true：文件工具只能访问 WorkDir 内；false：允许访问系统任意绝对路径（系统环境模式，仅受信任内网用）。
	SandboxEnabled bool `json:"sandbox_enabled"`

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

	// Extra 额外对接的远程 MCP 服务列表。主 MCP（local/remote）之外，
	// 这些服务的工具也会一并聚合暴露给大模型，按工具名自动路由调用。
	Extra []RemoteMCP `json:"extra"`
}

// RemoteMCP 一个额外对接的远程 MCP 服务配置。
type RemoteMCP struct {
	// Name 便于识别的名称（日志/调试用）。
	Name string `json:"name"`
	// BaseURL 远程地址，如 http://host:9000/mcp 或 .../sse。
	BaseURL string `json:"base_url"`
	// Transport "streamable-http"（默认）| "sse"。
	Transport string `json:"transport"`
	// APIKey 可选 Bearer 鉴权。
	APIKey string `json:"api_key"`
	// Headers 额外请求头。
	Headers map[string]string `json:"headers"`
}

// AgentConfig Agent 编排参数。
type AgentConfig struct {
	// MaxSteps ReAct 循环最大步数，防止死循环。
	MaxSteps int `json:"max_steps"`
	// 是否使用结构化（原生）工具调用；false 时退化为提示词约束的 JSON 工具调用。
	UseNativeTools bool `json:"use_native_tools"`
	// 单次工具返回结果最多保留多少行，避免上下文爆炸。
	MaxResultRows int `json:"max_result_rows"`
	// MemoryMaxHistory 单次最多回放的历史消息条数（上下文窗口），超出仅取最近部分。
	MemoryMaxHistory int `json:"memory_max_history"`
	// MemorySummaryThreshold 历史消息数达到该值时，对早期消息做摘要压缩。
	MemorySummaryThreshold int `json:"memory_summary_threshold"`
	// MemoryRecentKeep 摘要压缩时保留最近 N 条原文（其余早期消息被压缩为 summary）。
	MemoryRecentKeep int `json:"memory_recent_keep"`
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
	if c.Agent.MemoryMaxHistory == 0 {
		c.Agent.MemoryMaxHistory = 30
	}
	if c.Agent.MemorySummaryThreshold == 0 {
		c.Agent.MemorySummaryThreshold = 12
	}
	if c.Agent.MemoryRecentKeep == 0 {
		c.Agent.MemoryRecentKeep = 6
	}
	if c.Log.Dir == "" {
		c.Log.Dir = "logs"
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
3. 拿到查询结果后，用数据给出清晰、有洞察的分析结论（中文），并给出关键数字；
4. 当用户询问天气、气温、穿衣建议等时，使用 query_weather 工具查询实时天气；
5. 当结果适合可视化时，若工具列表中存在图表类工具（如 render_chart），则调用它生成图表，并【自动选择图表类型】：分类对比→bar 柱状图；时间/顺序趋势→line 折线图；占比构成(分类≤8)→pie 饼图。选好类型后再给出文字结论。

重要约束：
- 你自身没有任何内置技能，所有能力都来自后端 MCP 服务提供的工具（数据库查询、文件读写、天气、图表等）。请以工具列表中的实际定义为准；
- 你还可以用 MCP 提供的文件工具（read_file/write_file/append_file/list_dir/make_dir/delete_file/read_dir_tree）读写文件，用于查看配置/日志、导出分析报告等；
  · 默认启用工作目录沙箱：只能访问工作目录内，禁止越界；
  · 若后台关闭沙箱（系统环境模式），可访问服务器任意绝对路径，此时仅限受信任内网部署，请谨慎操作；
- 权限隔离、数据脱敏、危险 SQL 拦截、文件路径越界拦截都由后端 MCP 服务统一处理，你只需专注生成正确的分析与操作；
- 如果工具返回权限不足或报错，请如实告知用户原因，不要编造数据；
- 图表工具的 categories 与每个 series.data 必须长度一致、顺序对应，数值取自真实查询结果；
- 最终回答要面向业务，给出结论、数据支撑与建议；【不要展示原始 SQL 语句，SQL 仅作为工具调用过程使用】。`

// DefaultRemotePrompt 对接通用远程 MCP 服务时的默认系统提示词。
const DefaultRemotePrompt = `你是一个智能助手，可以调用多种工具来完成用户任务。
可用工具全部来自后端 MCP 服务（见下方工具列表，名称与参数以工具定义为准）。你自身没有任何其他内置技能，请以工具列表中的实际定义为准。

工作准则：
- 根据用户的自然语言问题，自行决定调用哪些工具，必要时可多次调用并组合结果；
- 当用户询问天气、气温、穿衣建议等时，使用 query_weather 工具查询实时天气；
- 拿到工具返回后，用中文给出清晰、有依据的结论，并引用关键数据；
- 如果工具返回错误或权限不足，请如实告知用户原因，不要编造数据；
- 若使用了图表类工具，其 categories 与每个 series.data 必须长度一致、顺序对应，数值取自真实查询结果；
- 除非用户明确要求，否则最终回答中不要展示原始 SQL 语句，SQL 仅作为工具调用过程使用；
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
			Mode:           "local",
			LocalEnabled:   true,
			RemoteEnabled:  false,
			ServerPath:     "../mcp-data-server/main.exe",
			Username:       "admin",
			Password:       "admin123",
			MaskEnabled:    true,
			SeedDemo:       true,
			SandboxEnabled: true, // 默认启用沙箱，文件工具只能访问 WorkDir 内
		},
		Agent: AgentConfig{
			MaxSteps:               8,
			UseNativeTools:         false,
			MaxResultRows:          200,
			MemoryMaxHistory:       30,
			MemorySummaryThreshold: 12,
			MemoryRecentKeep:       6,
		},
		Log: LogConfig{
			SaveToFile: false,
			Dir:        "logs",
		},
		Prompts: PromptsConfig{
			Builtin: DefaultBuiltinPrompt,
			Remote:  DefaultRemotePrompt,
		},
		UI: UIConfig{
			ShowDuration:  true,
			ShowSteps:     true,
			ShowImages:    true,
			Theme:         "dark",
			AppTitle:      "数据分析助手",
			AppSubtitle:   "自然语言 → LLM → MCP 权限 → SQL → 图表分析",
			WorkflowSteps: "自然语言 → LLM → MCP 权限 → SQL → 图表分析",
			AdminPageSize: 20,
			ChatPageSize:  50,
			PhoneRequired: true,
		},
	}
}
