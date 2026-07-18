package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"company.com/data-analysis-agent/config"
	"company.com/data-analysis-agent/llm"
	"company.com/data-analysis-agent/mcpclient"
)

// Agent 数据分析助手：把自然语言 -> 本地大模型 -> 生成 SQL -> MCP 权限处理 -> 查 MySQL -> 大模型分析 -> 输出。
type Agent struct {
	cfg     *config.Config
	llm     *llm.Client
	mcp     *mcpclient.Client
	builtin bool // 是否为内置 mcp-data-server（需要 token 注入与工具名映射）
	token   string
	tools   []llm.Tool
	schema  string

	// extraMCPs 额外对接的远程 MCP 客户端（与主 MCP 并存）。
	extraMCPs []*extraMCP
	// toolRoute 工具名 -> 提供该工具的额外 MCP 客户端。主 MCP 与内置 agent 工具不在此表内。
	toolRoute map[string]*mcpclient.Client
	// toolRegistry 工具名 -> 执行函数（picoclaw 风格的统一分发表）。
	toolRegistry map[string]toolRunner

	// mu 保护 cfg/llm/mcp/tools/schema 的热更新，避免与 Ask 并发竞争。
	mu sync.Mutex
}

// extraMCP 一个额外远程 MCP 连接及其元信息。
type extraMCP struct {
	name   string
	client *mcpclient.Client
}

// New 构造 Agent：按配置启动 MCP（本地子进程或远程服务）、登录、预加载表结构。
func New(cfg *config.Config) (*Agent, error) {
	a := &Agent{cfg: cfg}
	if err := a.initMCP(cfg); err != nil {
		return nil, err
	}
	return a, nil
}

// initMCP 依据配置建立 MCP 连接（本地子进程或远程服务），并完成登录与表结构预加载。
// 同时初始化 LLM 客户端与工具列表。可被 ApplyConfig 复用以热重建连接。
func (a *Agent) initMCP(cfg *config.Config) error {
	var mcp *mcpclient.Client
	var builtin bool
	var err error

	if strings.EqualFold(cfg.MCP.Mode, "remote") {
		// 远程 MCP 服务对接（无需本地子进程）
		builtin = false
		fmt.Printf("[agent] 使用远程 MCP 对接: %s (transport=%s)\n", cfg.MCP.BaseURL, transportName(cfg.MCP.Transport))
		mcp, err = mcpclient.StartRemote(mcpclient.RemoteConfig{
			BaseURL:   cfg.MCP.BaseURL,
			Transport: cfg.MCP.Transport,
			APIKey:    cfg.MCP.APIKey,
			Headers:   cfg.MCP.Headers,
			Timeout:   30 * time.Second,
		})
	} else {
		// 本地内置 mcp-data-server 子进程
		builtin = true
		fmt.Printf("[agent] 使用本地内置 MCP 对接: %s\n", cfg.MCP.ServerPath)
		mcp, err = mcpclient.Start(mcpclient.StartConfig{
			ServerPath:  cfg.MCP.ServerPath,
			DBDialect:   cfg.MCP.DBDialect,
			DBDsn:       cfg.MCP.DBDsn,
			Env:         cfg.MCP.Env,
			MaskEnabled: cfg.MCP.MaskEnabled,
			SeedDemo:    cfg.MCP.SeedDemo,
			WorkDir:     cfg.MCP.WorkDir,
		})
	}
	if err != nil {
		return err
	}

	a.mcp = mcp
	a.builtin = builtin
	a.llm = llm.NewClient(cfg.LLM.Provider, cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.Temperature, cfg.LLM.MaxTokens)

	// 内置模式：登录获取 token（后续所有 MCP 工具调用都需要）
	if builtin {
		if err := a.login(); err != nil {
			mcp.Close()
			return err
		}
	}

	// 连接额外对接的远程 MCP 服务（可选，多个并存）。
	a.connectExtraMCPs(cfg)

	// 定义暴露给大模型的工具
	a.tools = a.buildTools()

	// 内置模式：预加载常见表结构，注入系统提示
	if builtin {
		a.schema = a.loadSchema([]string{"customers", "orders", "users", "tenants", "audit_logs"})
	} else {
		a.schema = ""
	}
	return nil
}

// ApplyConfig 热更新配置：重建 LLM 客户端；若 MCP 相关配置发生变化则重建 MCP 连接。
// 调用方需保证在两次 Ask 之间或 Ask 持锁时调用，本方法自身加锁保证安全。
func (a *Agent) ApplyConfig(cfg *config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// LLM 配置总是重建（开销小、无副作用）。
	a.cfg.LLM = cfg.LLM
	a.llm = llm.NewClient(cfg.LLM.Provider, cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.Temperature, cfg.LLM.MaxTokens)
	a.cfg.Agent = cfg.Agent
	a.cfg.Prompts = cfg.Prompts

	// 仅当 MCP 相关配置发生变化时才重建连接（避免无谓地重启子进程）。
	if mcpConfigChanged(a.cfg.MCP, cfg.MCP) {
		fmt.Printf("[agent] 检测到 MCP 配置变化，重建 MCP 连接...\n")
		old := a.mcp
		if err := a.initMCP(cfg); err != nil {
			return err
		}
		a.cfg.MCP = cfg.MCP
		if old != nil {
			old.Close()
		}
	} else {
		// MCP 配置未变，仅同步其他字段（如凭据被后台修改也同步）。
		a.cfg.MCP = cfg.MCP
	}
	return nil
}

// transportName 返回传输方式的可读名称（默认 streamable-http）。
func transportName(t string) string {
	if strings.EqualFold(t, "sse") {
		return "sse"
	}
	return "streamable-http"
}

// mcpConfigChanged 判断两份 MCP 配置在影响连接的字段上是否不同（决定是否重建连接）。
func mcpConfigChanged(a, b config.MCPConfig) bool {
	return a.Mode != b.Mode ||
		a.ServerPath != b.ServerPath ||
		a.DBDialect != b.DBDialect ||
		a.DBDsn != b.DBDsn ||
		a.MaskEnabled != b.MaskEnabled ||
		a.SeedDemo != b.SeedDemo ||
		a.WorkDir != b.WorkDir ||
		a.Username != b.Username ||
		a.Password != b.Password ||
		a.BaseURL != b.BaseURL ||
		a.Transport != b.Transport ||
		a.APIKey != b.APIKey ||
		extraMCPChanged(a.Extra, b.Extra)
}

// extraMCPChanged 比较两组额外 MCP 配置是否不同（用 JSON 序列化简单比较）。
func extraMCPChanged(a, b []config.RemoteMCP) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) != string(bb)
}

// Close 释放资源。
func (a *Agent) Close() {
	if a.mcp != nil {
		a.mcp.Close()
	}
	for _, em := range a.extraMCPs {
		if em.client != nil {
			em.client.Close()
		}
	}
}

func (a *Agent) login() error {
	text, isErr, err := a.mcp.CallTool("auth_login", map[string]interface{}{
		"username": a.cfg.MCP.Username,
		"password": a.cfg.MCP.Password,
	})
	if err != nil {
		return fmt.Errorf("mcp auth_login: %w", err)
	}
	if isErr {
		return fmt.Errorf("auth_login failed: %s", text)
	}
	var res struct {
		Token     string `json:"token"`
		TenantID  string `json:"tenant_id"`
		Role      string `json:"role"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		return fmt.Errorf("parse login result: %w (raw: %s)", err, text)
	}
	if res.Token == "" {
		return fmt.Errorf("empty token from auth_login (raw: %s)", text)
	}
	a.token = res.Token
	fmt.Printf("[agent] 已以账号 %s 登录，角色=%s，token 已就绪\n", a.cfg.MCP.Username, res.Role)
	return nil
}

// connectExtraMCPs 连接配置中声明的额外远程 MCP 服务（失败仅告警，不阻断启动）。
func (a *Agent) connectExtraMCPs(cfg *config.Config) {
	// 关闭旧连接，避免热更新时泄漏。
	for _, em := range a.extraMCPs {
		if em.client != nil {
			em.client.Close()
		}
	}
	a.extraMCPs = nil
	for _, m := range cfg.MCP.Extra {
		if strings.TrimSpace(m.BaseURL) == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.BaseURL
		}
		cli, err := mcpclient.StartRemote(mcpclient.RemoteConfig{
			BaseURL:   m.BaseURL,
			Transport: m.Transport,
			APIKey:    m.APIKey,
			Headers:   m.Headers,
			Timeout:   30 * time.Second,
		})
		if err != nil {
			fmt.Printf("[agent] 额外 MCP [%s] 连接失败（已跳过）: %v\n", name, err)
			continue
		}
		fmt.Printf("[agent] 已对接额外 MCP [%s]: %s，发现 %d 个工具\n", name, m.BaseURL, len(cli.Tools()))
		a.extraMCPs = append(a.extraMCPs, &extraMCP{name: name, client: cli})
	}
}

// buildTools 暴露给大模型的工具。
// Agent 本身不内置任何技能工具，所有能力均来自 MCP：
// 主 MCP（内置 mcp-data-server 或远程通用 MCP）的工具清单 + 所有额外对接的远程 MCP 工具。
// Agent 只做编排与调度，不实现具体技能（图表/天气等也应由对应 MCP 提供）。
func (a *Agent) buildTools() []llm.Tool {
	var tools []llm.Tool
	if a.builtin {
		tools = append(tools, a.localDataTools()...)
	} else {
		for _, t := range a.mcp.Tools() {
			tools = append(tools, llm.Tool{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
	}

	// 聚合额外 MCP 工具并记录路由（同名工具以先注册者为准，跳过冲突）。
	a.toolRoute = make(map[string]*mcpclient.Client)
	existing := make(map[string]bool)
	for _, t := range tools {
		existing[t.Name] = true
	}
	for _, em := range a.extraMCPs {
		for _, t := range em.client.Tools() {
			if existing[t.Name] {
				fmt.Printf("[agent] 额外 MCP [%s] 的工具 %s 与已有工具重名，已跳过\n", em.name, t.Name)
				continue
			}
			existing[t.Name] = true
			a.toolRoute[t.Name] = em.client
			tools = append(tools, llm.Tool{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
	}
	// 构建「工具名 -> 执行函数」注册表，供 executeTool 按名分发。
	a.toolRegistry = a.buildRegistry(tools)
	return tools
}

// localDataTools 本地内置 mcp-data-server 的数据库分析工具（带中文描述与映射）。
func (a *Agent) localDataTools() []llm.Tool {
	return []llm.Tool{
		{
			Name:        "describe_table",
			Description: "查看某张数据表的字段结构（列名）。在编写 SQL 前先了解表结构。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"table": map[string]interface{}{"type": "string", "description": "表名，如 customers / orders"},
				},
				"required": []string{"table"},
			},
		},
		{
			Name:        "query_data",
			Description: "结构化安全查询（推荐给非管理员角色）：按表名+字段+过滤条件查询，自动叠加租户/区域/门店隔离并对敏感字段脱敏。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"table":   map[string]interface{}{"type": "string", "description": "表名: customers | orders"},
					"fields":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "返回字段，留空返回全部"},
					"filters": map[string]interface{}{"type": "object", "description": "等值过滤，如 {\"status\":\"paid\"}"},
					"order":   map[string]interface{}{"type": "string", "description": "排序，如 amount desc"},
					"limit":   map[string]interface{}{"type": "integer", "description": "返回行数上限，默认100，最大1000"},
				},
				"required": []string{"table"},
			},
		},
		{
			Name:        "run_sql",
			Description: "执行原生只读 SQL（仅平台运营 super_admin 可用）。用于复杂分析（聚合、联表、分组统计）。MCP 会自动拦截危险关键字并做权限/审计。优先使用 SELECT。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"sql": map[string]interface{}{"type": "string", "description": "SELECT 语句，例如 SELECT status, COUNT(*) AS cnt, SUM(amount) AS total FROM orders GROUP BY status"},
				},
				"required": []string{"sql"},
			},
		},
		// --- 文件 / 目录读写（由内置 mcp-data-server 提供，沙箱在 work_dir 内） ---
		{
			Name:        "read_file",
			Description: "读取文本文件内容（路径相对于 MCP 工作目录沙箱）。用于查看配置文件、日志、导出的数据等。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":      map[string]interface{}{"type": "string", "description": "相对工作目录的文件路径，如 reports/summary.txt"},
					"max_bytes": map[string]interface{}{"type": "integer", "description": "最多读取字节数，默认 65536，最大 1048576"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: "写入文本文件（覆盖，父目录自动创建）。用于生成分析报告、导出查询结果。路径相对于工作目录。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":    map[string]interface{}{"type": "string", "description": "相对工作目录的文件路径"},
					"content": map[string]interface{}{"type": "string", "description": "要写入的文本"},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "append_file",
			Description: "向文件末尾追加文本（不存在则创建）。用于日志累积、结果追加。路径相对于工作目录。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":    map[string]interface{}{"type": "string", "description": "相对工作目录的文件路径"},
					"content": map[string]interface{}{"type": "string", "description": "要追加的文本"},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "list_dir",
			Description: "列出目录下的文件与子目录（含名称/类型/大小/修改时间）。路径相对于工作目录，留空=根目录。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "相对工作目录的目录路径，留空=根目录"},
				},
			},
		},
		{
			Name:        "make_dir",
			Description: "创建目录（含多级父目录）。路径相对于工作目录。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "相对工作目录的目录路径"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "delete_file",
			Description: "删除一个文件（不会删除目录）。路径相对于工作目录。删除前确认路径正确。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "相对工作目录的文件路径"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "read_dir_tree",
			Description: "递归列出目录树（最多两层）。用于了解工作目录整体结构。路径相对于工作目录，留空=根目录。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "相对工作目录的起始目录，留空=根目录"},
				},
			},
		},
	}
}

// loadSchema 预加载表结构，拼成系统提示片段。
func (a *Agent) loadSchema(tables []string) string {
	var sb strings.Builder
	sb.WriteString("已知数据库表结构如下（如缺失可用 describe_table 工具补充）：\n")
	found := false
	for _, t := range tables {
		text, isErr, err := a.mcp.CallTool("describe_table", map[string]interface{}{
			"token": a.token,
			"table": t,
		})
		if err != nil || isErr || text == "" {
			continue
		}
		sb.WriteString("- " + t + ": " + text + "\n")
		found = true
	}
	if !found {
		sb.Reset()
		sb.WriteString("")
	}
	return sb.String()
}

// systemPrompt 系统提示词（从配置读取，支持后台热更新）。
func (a *Agent) systemPrompt() string {
	if a.builtin {
		p := a.cfg.Prompts.Builtin
		if p == "" {
			p = config.DefaultBuiltinPrompt
		}
		if a.schema != "" {
			p += "\n\n" + a.schema
		}
		return p
	}
	p := a.cfg.Prompts.Remote
	if p == "" {
		p = config.DefaultRemotePrompt
	}
	return p
}

// AskOptions 单次提问的可选覆盖项（来自 Web UI 的"基础设置"）。
// 字段为空/零值表示沿用运行配置，不覆盖。
type AskOptions struct {
	Model       string  `json:"model"`        // 覆盖模型名
	Temperature float64 `json:"temperature"`  // 覆盖生成温度（<=0 表示沿用）
	MaxTokens   int     `json:"max_tokens"`   // 覆盖单次生成上限（<=0 表示沿用）
}

// LLMInfo 返回当前生效的 LLM 配置摘要，供前端"基础设置"初始化。
func (a *Agent) LLMInfo() map[string]interface{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]interface{}{
		"provider":    a.cfg.LLM.Provider,
		"base_url":    a.cfg.LLM.BaseURL,
		"model":       a.cfg.LLM.Model,
		"temperature": a.cfg.LLM.Temperature,
		"max_tokens":  a.cfg.LLM.MaxTokens,
	}
}

// Ask 处理一次用户提问，返回最终分析文本（CLI 使用）。
func (a *Agent) Ask(question string) (string, error) {
	return a.AskWith(question, nil)
}

// AskWith 同 Ask，但允许传入单次覆盖项（CLI 的基础设置：模型/温度/max_tokens）。
func (a *Agent) AskWith(question string, opts *AskOptions) (string, error) {
	res, err := a.AskRich(question, opts)
	if err != nil {
		return "", err
	}
	return res.Answer, nil
}

// HistoryMessage 一条历史对话消息（用于多轮上下文记忆）。
// 兼容旧调用：仅含文本。新代码建议用 HistoryItem（含结构化 extra）。
type HistoryMessage struct {
	Role    string // user | assistant
	Content string
}

// AskRich 处理一次用户提问，返回结构化结果（含图表/数据/SQL/步骤），供 HTTP 前端使用。
// opts 为可选的单次覆盖项（模型/温度/max_tokens）；为 nil 时完全沿用运行配置。
func (a *Agent) AskRich(question string, opts *AskOptions) (*AskResult, error) {
	return a.AskRichWithHistory(nil, question, opts)
}

// AskRichWithHistory 在带历史上下文的情况下处理一次提问，实现多轮对话记忆。
// history 为按时间正序排列的既往消息（不含本次 question），可携带结构化 extra（图表/表格/SQL）。
func (a *Agent) AskRichWithHistory(history []HistoryItem, question string, opts *AskOptions) (*AskResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 若前端携带覆盖项，则本次使用一个临时 LLM 客户端，不影响全局运行配置。
	llmClient := a.llm
	if opts != nil && (opts.Model != "" || opts.Temperature > 0 || opts.MaxTokens > 0) {
		model := a.cfg.LLM.Model
		temp := a.cfg.LLM.Temperature
		maxTok := a.cfg.LLM.MaxTokens
		if opts.Model != "" {
			model = opts.Model
		}
		if opts.Temperature > 0 {
			temp = opts.Temperature
		}
		if opts.MaxTokens > 0 {
			maxTok = opts.MaxTokens
		}
		llmClient = llm.NewClient(a.cfg.LLM.Provider, a.cfg.LLM.BaseURL, model, a.cfg.LLM.APIKey, temp, maxTok)
	}

	system := a.systemPrompt()
	messages := []llm.Message{
		{Role: "system", Content: system},
	}

	// 记忆层：组织历史上下文（结构化回放 + 早期摘要压缩）。
	var summary string
	var histMsgs []llm.Message
	if len(history) > 0 {
		summary, histMsgs = a.buildMemoryContext(history, defaultMemoryConfig(), llmClient)
	}
	if summary != "" {
		memPrompt := "以下是本次对话较早阶段的记忆摘要，请结合它理解用户的连续意图：\n" + summary
		// 把记忆摘要作为一条 system 消息追加在系统提示之后、历史之前。
		messages = append(messages, llm.Message{Role: "system", Content: memPrompt})
	}
	messages = append(messages, histMsgs...)
	messages = append(messages, llm.Message{Role: "user", Content: question})
	result := &AskResult{}

	for step := 0; step < a.cfg.Agent.MaxSteps; step++ {
		var resp *llm.Response
		var err error
		if a.cfg.Agent.UseNativeTools {
			resp, err = llmClient.Chat(messages, a.tools)
		} else {
			resp, err = llmClient.Chat(messages, nil)
			// 退化模式：尝试从文本中解析工具调用
			if err == nil {
				resp = a.parseFallbackToolCall(resp, &messages)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("llm chat: %w", err)
		}

		// 没有工具调用 -> 视为最终回答
		if len(resp.ToolCalls) == 0 {
			result.Answer = strings.TrimSpace(resp.Content)
			return result, nil
		}

		// 执行所有工具调用，结果作为 tool 消息回灌
		for _, tc := range resp.ToolCalls {
			fmt.Printf("[agent] 模型请求调用工具: %s 参数=%s\n", tc.Name, tc.Arguments)
			toolResult := a.executeTool(tc, result)
			fmt.Printf("[agent] 工具返回(已截断): %s\n", truncate(toolResult, 300))
			result.Steps = append(result.Steps, StepLog{Tool: tc.Name, Args: tc.Arguments, Result: truncate(toolResult, 500)})
			messages = append(messages,
				llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: []llm.ToolCall{tc}},
				llm.Message{Role: "tool", Content: toolResult, ToolCallID: tc.ID, Name: tc.Name},
			)
		}
	}
	return nil, fmt.Errorf("已达到最大推理步数 %d，仍未给出最终结论", a.cfg.Agent.MaxSteps)
}

// executeTool 执行一个工具调用：解析参数后按工具名在注册表中查找并执行（picoclaw 风格统一分发）。
func (a *Agent) executeTool(tc llm.ToolCall, result *AskResult) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		return fmt.Sprintf("工具参数解析失败: %v", err)
	}
	run, ok := a.toolRegistry[tc.Name]
	if !ok {
		return fmt.Sprintf("未知工具: %s", tc.Name)
	}
	text, err := run(args, result)
	if err != nil {
		return "工具执行失败: " + err.Error()
	}
	return text
}

// parseRows 尝试把工具返回文本解析为行数据（JSON 数组）。
func parseRows(text string) []map[string]interface{} {
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &rows); err == nil && len(rows) > 0 {
		return rows
	}
	return nil
}

// mcpToolName 将 Agent 暴露给模型的工具名翻译为 mcp-data-server 的真实工具名。
func (a *Agent) mcpToolName(name string) string {
	switch name {
	case "query_data":
		return "query_table"
	default:
		return name // run_sql / describe_table 同名
	}
}

// truncateResult 对工具返回结果做行数/长度截断，避免上下文爆炸。
func (a *Agent) truncateResult(text string) string {
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &rows); err == nil && len(rows) > 0 {
		limit := a.cfg.Agent.MaxResultRows
		if len(rows) > limit {
			rows = rows[:limit]
			note := map[string]interface{}{"__note": fmt.Sprintf("结果过多，仅展示前 %d 行", limit)}
			rows = append(rows, note)
		}
		if b, err := json.MarshalIndent(rows, "", "  "); err == nil {
			return string(b)
		}
	}
	return truncate(text, 8000)
}

// parseFallbackToolCall 退化模式：从模型文本中解析 ```json 工具调用块。
// 期望格式：{"tool":"run_sql","args":{"sql":"..."}}，最终答案以 ANSWER: 开头。
func (a *Agent) parseFallbackToolCall(resp *llm.Response, messages *[]llm.Message) *llm.Response {
	content := resp.Content
	idx := strings.Index(content, "```json")
	if idx < 0 {
		return resp
	}
	start := idx + len("```json")
	end := strings.Index(content[start:], "```")
	if end < 0 {
		return resp
	}
	block := strings.TrimSpace(content[start : start+end])
	var call struct {
		Tool string                 `json:"tool"`
		Args map[string]interface{} `json:"args"`
	}
	if err := json.Unmarshal([]byte(block), &call); err != nil {
		return resp
	}
	if call.Tool == "" {
		return resp
	}
	argsB, _ := json.Marshal(call.Args)
	newResp := &llm.Response{
		Content: strings.TrimSpace(strings.Replace(content, content[idx:start+end+3], "", 1)),
		ToolCalls: []llm.ToolCall{{
			Name:      call.Tool,
			Arguments: string(argsB),
		}},
	}
	return newResp
}

// truncate 文本截断辅助。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("... (已截断，共 %d 字符)", len(r))
}
