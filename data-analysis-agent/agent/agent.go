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

	// mu 保护 cfg/llm/mcp/tools/schema 的热更新，避免与 Ask 并发竞争。
	mu sync.Mutex
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
		a.Username != b.Username ||
		a.Password != b.Password ||
		a.BaseURL != b.BaseURL ||
		a.Transport != b.Transport ||
		a.APIKey != b.APIKey
}

// Close 释放资源。
func (a *Agent) Close() {
	if a.mcp != nil {
		a.mcp.Close()
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

// buildTools 暴露给大模型的工具。
// 内置 agent 工具（render_chart / query_weather）始终可用；
// 数据类工具则依对接模式而定：内置 mcp-data-server 用写死的三个，远程 MCP 用其真实工具清单。
func (a *Agent) buildTools() []llm.Tool {
	tools := a.builtinAgentTools()
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
	return tools
}

// builtinAgentTools Agent 内置工具（不论 MCP 模式都可用）。
func (a *Agent) builtinAgentTools() []llm.Tool {
	return []llm.Tool{
		{
			Name:        "render_chart",
			Description: "当分析结果适合可视化时调用，生成图表供前端展示。你需要从查询结果里提取聚合后的数值填入。适合展示分组对比、占比、趋势。调用后请再用一句话给出文字结论。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":       map[string]interface{}{"type": "string", "enum": []string{"bar", "line", "pie"}, "description": "图表类型：bar 柱状(对比) | line 折线(趋势) | pie 饼图(占比)"},
					"title":      map[string]interface{}{"type": "string", "description": "图表标题"},
					"categories": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "X 轴分类或饼图各扇区名称，如 [\"paid\",\"pending\",\"cancelled\"]"},
					"series": map[string]interface{}{
						"type":        "array",
						"description": "数据系列。饼图只需一个系列，data 与 categories 一一对应。",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"name": map[string]interface{}{"type": "string", "description": "系列名，如 订单数 / 金额"},
								"data": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "number"}, "description": "数值数组，与 categories 顺序一致"},
							},
							"required": []string{"name", "data"},
						},
					},
				},
				"required": []string{"type", "categories", "series"},
			},
		},
		{
			Name:        "query_weather",
			Description: "联网查询某个城市的实时天气（气温、天气状况、湿度、风速）。当用户问到天气、气温、是否下雨、出行建议等时调用。返回中文天气描述。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{"type": "string", "description": "城市名，如 北京 / 上海 / 杭州 / 东京"},
				},
				"required": []string{"city"},
			},
		},
	}
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

// Ask 处理一次用户提问，返回最终分析文本（CLI 使用）。
func (a *Agent) Ask(question string) (string, error) {
	res, err := a.AskRich(question)
	if err != nil {
		return "", err
	}
	return res.Answer, nil
}

// AskRich 处理一次用户提问，返回结构化结果（含图表/数据/SQL/步骤），供 HTTP 前端使用。
func (a *Agent) AskRich(question string) (*AskResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	messages := []llm.Message{
		{Role: "system", Content: a.systemPrompt()},
		{Role: "user", Content: question},
	}
	result := &AskResult{}

	for step := 0; step < a.cfg.Agent.MaxSteps; step++ {
		var resp *llm.Response
		var err error
		if a.cfg.Agent.UseNativeTools {
			resp, err = a.llm.Chat(messages, a.tools)
		} else {
			resp, err = a.llm.Chat(messages, nil)
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

// executeTool 执行一个工具调用；render_chart 由 Agent 本地捕获，其余注入 token 后转发给 MCP。
func (a *Agent) executeTool(tc llm.ToolCall, result *AskResult) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		return fmt.Sprintf("工具参数解析失败: %v", err)
	}

	// render_chart 不走 MCP，直接解析为图表规格存入结果。
	if tc.Name == "render_chart" {
		return a.captureChart(tc.Arguments, result)
	}

	// query_weather 联网天气查询，由 Agent 本地发起 HTTP 请求，不走 MCP。
	if tc.Name == "query_weather" {
		city, _ := args["city"].(string)
		desc, err := a.queryWeather(city)
		if err != nil {
			return "天气查询失败: " + err.Error()
		}
		return desc
	}

	// 内置 mcp-data-server 需要注入 token 并把模型工具名映射到后端真实名；
	// 远程通用 MCP 直接以模型给出的工具名转发，不注入 token。
	var mcpName string
	if a.builtin {
		args["token"] = a.token
		mcpName = a.mcpToolName(tc.Name)
	} else {
		mcpName = tc.Name
	}
	if !a.mcp.HasTool(mcpName) {
		return fmt.Sprintf("未知工具: %s", mcpName)
	}

	text, isErr, err := a.mcp.CallTool(mcpName, args)
	if err != nil {
		return fmt.Sprintf("工具调用出错: %v", err)
	}
	if isErr {
		return "工具执行失败: " + text
	}

	// 记录 SQL 与返回的数据行，供前端表格/图表兜底展示（仅内置模式有 run_sql）。
	if a.builtin && tc.Name == "run_sql" {
		if sql, ok := args["sql"].(string); ok {
			result.SQL = sql
		}
	}
	if rows := parseRows(text); rows != nil {
		result.Rows = rows
	}
	return a.truncateResult(text)
}

// captureChart 解析 render_chart 参数为 ChartSpec 并存入结果。
func (a *Agent) captureChart(rawArgs string, result *AskResult) string {
	var spec ChartSpec
	if err := json.Unmarshal([]byte(rawArgs), &spec); err != nil {
		return fmt.Sprintf("图表参数解析失败: %v", err)
	}
	if spec.Type == "" {
		spec.Type = "bar"
	}
	if len(spec.Series) == 0 || len(spec.Categories) == 0 {
		return "图表数据不完整：categories 与 series 均不能为空"
	}
	result.Chart = &spec
	return "图表已生成并展示给用户，请再用简洁的中文给出这组数据的分析结论。"
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
