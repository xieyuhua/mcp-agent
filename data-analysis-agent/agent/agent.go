package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"company.com/data-analysis-agent/config"
	"company.com/data-analysis-agent/internal/logger"
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
	mu sync.RWMutex

	// callLogStore 调用日志存储（可选，HTTP 服务模式下注入 userdb）。
	callLogStore CallLogStore
}

// CallLogStore 调用日志存储接口，由 userdb.Store 实现。
type CallLogStore interface {
	InsertLLMCallLog(userID, convID, model, provider, messages, tools, response string, durationMs int64, errorMsg string) error
	InsertMCPCallLog(userID, convID, toolName, serverName, args, result string, durationMs int64, isErr bool, errorMsg string) error
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

// initMCP 依据配置建立 MCP 连接。本地 MCP（内置 mcp-data-server 子进程）与远程 MCP
// （HTTP 服务）为两套相互独立的对接，可各自独立开关：
//   - 两者都开：本地作为主 MCP（builtin 模式：token 注入 + 工具名映射），远程并入额外 MCP 聚合；
//   - 仅本地：本地作为主 MCP；
//   - 仅远程：远程作为主 MCP（非 builtin）；
//   - 都关：报错，要求至少启用其一。
// 同时初始化 LLM 客户端与工具列表。可被 ApplyConfig 复用以热重建连接。
func (a *Agent) initMCP(cfg *config.Config) error {
	localOn, remoteOn := resolveMCPFlags(cfg.MCP)

	var mainMCP *mcpclient.Client
	var builtin bool
	var err error

	if localOn {
		// 本地内置 mcp-data-server 始终作为主 MCP（builtin 模式）。
		builtin = true
		logger.Infof("[agent] 启用本地内置 MCP 对接: %s", cfg.MCP.ServerPath)
		mainMCP, err = mcpclient.Start(mcpclient.StartConfig{
			ServerPath:     cfg.MCP.ServerPath,
			DBDialect:      cfg.MCP.DBDialect,
			DBDsn:          cfg.MCP.DBDsn,
			Env:            cfg.MCP.Env,
			MaskEnabled:    cfg.MCP.MaskEnabled,
			SeedDemo:       cfg.MCP.SeedDemo,
			WorkDir:        cfg.MCP.WorkDir,
			SandboxEnabled: cfg.MCP.SandboxEnabled,
		})
		if err != nil {
			return err
		}
	} else if remoteOn {
		// 仅远程：远程作为主 MCP（非 builtin）。
		builtin = false
		logger.Infof("[agent] 仅启用远程 MCP 对接: %s (transport=%s)", cfg.MCP.BaseURL, transportName(cfg.MCP.Transport))
		mainMCP, err = mcpclient.StartRemote(mcpclient.RemoteConfig{
			BaseURL:   cfg.MCP.BaseURL,
			Transport: cfg.MCP.Transport,
			APIKey:    cfg.MCP.APIKey,
			Headers:   cfg.MCP.Headers,
			Timeout:   30 * time.Second,
		})
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("MCP 未启用：请至少开启本地或远程 MCP 之一（mcp.local_enabled / mcp.remote_enabled）")
	}

	a.mcp = mainMCP
	a.builtin = builtin
	a.llm = llm.NewClient(cfg.LLM.Provider, cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.Temperature, cfg.LLM.MaxTokens)
	a.llm.OnLog = a.buildLLMLogCallback()

	// 内置模式：登录获取 token（后续所有 MCP 工具调用都需要）
	if builtin {
		if err := a.login(); err != nil {
			mainMCP.Close()
			return err
		}
	}

	// 连接额外对接的远程 MCP 服务（可选，多个并存）。
	// 当本地已作为主 MCP 且远程也开启时，把主远程服务也并入额外列表一并聚合。
	a.connectExtraMCPs(cfg, localOn && remoteOn)

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

// resolveMCPFlags 返回本地/远程 MCP 的实际开关状态。
// 兼容旧配置：当两个开关都为 false 时，按 Mode 决定（local=仅本地，remote=仅远程）。
func resolveMCPFlags(m config.MCPConfig) (localOn, remoteOn bool) {
	localOn = m.LocalEnabled
	remoteOn = m.RemoteEnabled
	if !localOn && !remoteOn {
		if strings.EqualFold(m.Mode, "remote") {
			remoteOn = true
		} else {
			localOn = true
		}
	}
	return
}

// SetCallLogStore 设置调用日志存储（HTTP 服务模式下注入 userdb）。
func (a *Agent) SetCallLogStore(store CallLogStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.callLogStore = store
}

// ApplyConfig 热更新配置：重建 LLM 客户端；若 MCP 相关配置发生变化则重建 MCP 连接。
// 调用方需保证在两次 Ask 之间或 Ask 持锁时调用，本方法自身加锁保证安全。
func (a *Agent) ApplyConfig(cfg *config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// LLM 配置总是重建（开销小、无副作用）。
	a.cfg.LLM = cfg.LLM
	a.llm = llm.NewClient(cfg.LLM.Provider, cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.Temperature, cfg.LLM.MaxTokens)
	a.llm.OnLog = a.buildLLMLogCallback()
	a.cfg.Agent = cfg.Agent
	a.cfg.Prompts = cfg.Prompts
	a.cfg.UI = cfg.UI

	// 仅当 MCP 相关配置发生变化时才重建连接（避免无谓地重启子进程）。
	if mcpConfigChanged(a.cfg.MCP, cfg.MCP) {
		logger.Infof("[agent] 检测到 MCP 配置变化，重建 MCP 连接...")
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

	// 日志开关热更新：后台修改“保存日志到文件”即时生效，无需重启。
	logger.SetSaveToFile(cfg.Log.SaveToFile)
	logger.SetDir(cfg.Log.Dir)
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
		a.LocalEnabled != b.LocalEnabled ||
		a.RemoteEnabled != b.RemoteEnabled ||
		a.ServerPath != b.ServerPath ||
		a.DBDialect != b.DBDialect ||
		a.DBDsn != b.DBDsn ||
		a.MaskEnabled != b.MaskEnabled ||
		a.SeedDemo != b.SeedDemo ||
		a.WorkDir != b.WorkDir ||
		a.SandboxEnabled != b.SandboxEnabled ||
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
	}, nil)
	if err != nil {
		return fmt.Errorf("mcp auth_login: %w", err)
	}
	if isErr {
		return fmt.Errorf("auth_login failed: %s", text)
	}
	var res struct {
		Token    string `json:"token"`
		TenantID string `json:"tenant_id"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		return fmt.Errorf("parse login result: %w (raw: %s)", err, text)
	}
	if res.Token == "" {
		return fmt.Errorf("empty token from auth_login (raw: %s)", text)
	}
	a.token = res.Token
	logger.Infof("[agent] MCP 登录成功: 账号=%s 角色=%s", a.cfg.MCP.Username, res.Role)
	return nil
}

// connectExtraMCPs 连接配置中声明的额外远程 MCP 服务（失败仅告警，不阻断启动）。
// includeMainRemote=true 时，主远程 MCP（cfg.MCP.BaseURL）也会并入额外列表
// （用于“本地+远程同时开启”场景：本地是主 MCP，远程作为额外 MCP 聚合工具）。
func (a *Agent) connectExtraMCPs(cfg *config.Config, includeMainRemote bool) {
	// 关闭旧连接，避免热更新时泄漏。
	for _, em := range a.extraMCPs {
		if em.client != nil {
			em.client.Close()
		}
	}
	a.extraMCPs = nil

	// 主远程 MCP（仅当本地已作为主 MCP 时并入）。
	if includeMainRemote && strings.TrimSpace(cfg.MCP.BaseURL) != "" {
		cli, err := mcpclient.StartRemote(mcpclient.RemoteConfig{
			BaseURL:   cfg.MCP.BaseURL,
			Transport: cfg.MCP.Transport,
			APIKey:    cfg.MCP.APIKey,
			Headers:   cfg.MCP.Headers,
			Timeout:   30 * time.Second,
		})
		if err != nil {
			logger.Warnf("[agent] 主远程 MCP 连接失败（已跳过）: %v", err)
		} else {
			logger.Infof("[agent] 已对接主远程 MCP: %s，发现 %d 个工具: %s", cfg.MCP.BaseURL, len(cli.Tools()), func() string {
				names := make([]string, 0, len(cli.Tools()))
				for _, t := range cli.Tools() {
					names = append(names, t.Name)
				}
				return strings.Join(names, ", ")
			}())
			a.extraMCPs = append(a.extraMCPs, &extraMCP{name: "remote", client: cli})
		}
	}

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
			logger.Warnf("[agent] 额外 MCP [%s] 连接失败（已跳过）: %v", name, err)
			continue
		}
		logger.Infof("[agent] 已对接额外 MCP [%s]: %s，发现 %d 个工具: %s", name, m.BaseURL, len(cli.Tools()), func() string {
			names := make([]string, 0, len(cli.Tools()))
			for _, t := range cli.Tools() {
				names = append(names, t.Name)
			}
			return strings.Join(names, ", ")
		}())
		a.extraMCPs = append(a.extraMCPs, &extraMCP{name: name, client: cli})
	}
}

// buildTools 暴露给大模型的工具。
// 主 MCP 工具（内置 mcp-data-server 或远程通用 MCP）+ 额外对接的远程 MCP 工具
// 以及 Agent 本地内置的 render_chart（图表规格）。
// Agent 只做编排与调度，不实现具体技能（所有能力均由 MCP 提供）。
func (a *Agent) buildTools() []llm.Tool {
	var tools []llm.Tool
	if a.builtin {
		// 本地内置模式：保留 LLM 友好的中文描述与工具名映射，同时从 mcp-data-server 发现并补充新增工具。
		tools = append(tools, a.localDataTools()...)
		existing := make(map[string]bool, len(tools))
		for _, t := range tools {
			existing[t.Name] = true
		}
		for _, t := range a.mcp.Tools() {
			if existing[t.Name] {
				continue
			}
			existing[t.Name] = true
			tools = append(tools, llm.Tool{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
			logger.Infof("[agent] 从本地 MCP 发现并补充工具: %s", t.Name)
		}
	} else {
		remoteTools := a.mcp.Tools()
		for _, t := range remoteTools {
			tools = append(tools, llm.Tool{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
		// 远程 MCP 可能未提供图表工具，补充 Agent 内置兜底。
		existing := make(map[string]bool, len(tools))
		for _, t := range tools {
			existing[t.Name] = true
		}
		if !existing["render_chart"] {
			tools = append(tools, llm.Tool{
				Name:        "render_chart",
				Description: "根据数据生成图表规格（bar/line/pie），供前端 Canvas 渲染。当查询结果适合可视化时，先调用此工具生成图表，再给出文字结论。categories 与每个 series.data 必须长度一致、顺序对应。",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"type":       map[string]interface{}{"type": "string", "description": "图表类型: bar(柱状) | line(折线) | pie(饼图), 分类对比选 bar, 时间趋势选 line, 占比构成(≤8类)选 pie", "enum": []string{"bar", "line", "pie"}},
						"title":      map[string]interface{}{"type": "string", "description": "图表标题"},
						"categories": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "X 轴分类（柱状/折线）或饼图各扇区标签"},
						"series": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"name": map[string]interface{}{"type": "string", "description": "系列名称"},
									"data": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "number"}, "description": "数值数组，长度必须与 categories 一致"},
								},
								"required": []string{"name", "data"},
							},
							"description": "数据系列；饼图只取第一个系列",
						},
					},
					"required": []string{"type", "title", "categories", "series"},
				},
			})
		}
		logger.Infof("[agent] 远程 MCP 主服务返回 %d 个工具: %s", len(remoteTools), func() string {
			names := make([]string, 0, len(remoteTools))
			for _, t := range remoteTools {
				names = append(names, t.Name)
			}
			return strings.Join(names, ", ")
		}())
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
				logger.Warnf("[agent] 额外 MCP [%s] 的工具 %s 与已有工具重名，已跳过", em.name, t.Name)
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
	logger.Infof("[agent] 已加载 %d 个工具供 LLM 使用: %s", len(tools), toolNames(tools))
	return tools
}

// toolNames 返回工具列表的名称串（用于日志）。
func toolNames(tools []llm.Tool) string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}

// localDataTools 本地内置 mcp-data-server 的数据库分析工具（带中文描述与映射）。
func (a *Agent) localDataTools() []llm.Tool {
	tools := make([]llm.Tool, 0, 10) // 内置工具数量固定，预分配容量避免扩容
	tools = append(tools,
		llm.Tool{
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
		llm.Tool{
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
		llm.Tool{
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
		llm.Tool{
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
		llm.Tool{
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
		llm.Tool{
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
		llm.Tool{
			Name:        "list_dir",
			Description: "列出目录下的文件与子目录（含名称/类型/大小/修改时间）。路径相对于工作目录，留空=根目录。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "相对工作目录的目录路径，留空=根目录"},
				},
			},
		},
		llm.Tool{
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
		llm.Tool{
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
		llm.Tool{
			Name:        "read_dir_tree",
			Description: "递归列出目录树（最多两层）。用于了解工作目录整体结构。路径相对于工作目录，留空=根目录。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "相对工作目录的起始目录，留空=根目录"},
				},
			},
		},
		// --- 联网查询（由内置 mcp-data-server 提供，无需 API key） ---
		llm.Tool{
			Name:        "web_search",
			Description: "联网搜索（基于 DuckDuckGo，无需 API key）。返回相关网页的标题、链接与摘要，用于获取实时或外部信息（如最新新闻、公开资料等）。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "搜索关键词，如「2024 年中国 GDP 增速」"},
					"limit": map[string]interface{}{"type": "integer", "description": "返回结果条数，默认5，最大10"},
				},
				"required": []string{"query"},
			},
		},
		llm.Tool{
			Name:        "web_fetch",
			Description: "抓取指定网页 URL 并提取正文纯文本（自动去除脚本/样式噪声）。用于读取搜索结果的具体内容、新闻正文、公开文档。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url":       map[string]interface{}{"type": "string", "description": "目标网页地址，需以 http:// 或 https:// 开头"},
					"max_chars": map[string]interface{}{"type": "integer", "description": "返回正文最大字符数，默认8000，最大40000"},
				},
				"required": []string{"url"},
			},
		},
	)
	tools = append(tools,
		llm.Tool{
			Name:        "render_chart",
			Description: "根据数据生成图表规格（bar/line/pie），供前端 Canvas 渲染。当查询结果适合可视化时，先调用此工具生成图表，再给出文字结论。categories 与每个 series.data 必须长度一致、顺序对应。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":       map[string]interface{}{"type": "string", "description": "图表类型: bar(柱状) | line(折线) | pie(饼图), 分类对比选 bar, 时间趋势选 line, 占比构成(≤8类)选 pie", "enum": []string{"bar", "line", "pie"}},
					"title":      map[string]interface{}{"type": "string", "description": "图表标题"},
					"categories": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "X 轴分类（柱状/折线）或饼图各扇区标签"},
					"series": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"name": map[string]interface{}{"type": "string", "description": "系列名称"},
								"data": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "number"}, "description": "数值数组，长度必须与 categories 一致"},
							},
							"required": []string{"name", "data"},
						},
						"description": "数据系列；饼图只取第一个系列",
					},
				},
				"required": []string{"type", "title", "categories", "series"},
			},
		},
	)
	return tools
}

// renderChartLocal 在 Agent 本地生成图表规格（无需 MCP 后端），并把规格写入结果供前端渲染。
func renderChartLocal(args map[string]interface{}, result *AskResult) (string, error) {
	chartType, _ := args["type"].(string)
	if chartType == "" {
		chartType = "bar"
	}
	title, _ := args["title"].(string)
	var categories []string
	if c, ok := args["categories"].([]interface{}); ok {
		for _, v := range c {
			if s, ok := v.(string); ok {
				categories = append(categories, s)
			}
		}
	}
	var series []ChartSeries
	if s, ok := args["series"].([]interface{}); ok {
		for _, si := range s {
			m, ok := si.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			var data []float64
			if d, ok := m["data"].([]interface{}); ok {
				for _, di := range d {
					switch v := di.(type) {
					case float64:
						data = append(data, v)
					case float32:
						data = append(data, float64(v))
					case int:
						data = append(data, float64(v))
					case int64:
						data = append(data, float64(v))
					case json.Number:
						f, _ := v.Float64()
						data = append(data, f)
					}
				}
			}
			series = append(series, ChartSeries{Name: name, Data: data})
		}
	}
	if len(categories) == 0 || len(series) == 0 {
		return "图表参数不完整", nil
	}
	result.Chart = &ChartSpec{Type: chartType, Title: title, Categories: categories, Series: series}
	b, _ := json.Marshal(map[string]interface{}{"chart": result.Chart})
	return string(b), nil
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
		}, nil)
		if err != nil || isErr || text == "" {
			continue
		}
		sb.WriteString("- " + t + ": " + text + "\n")
		found = true
	}
	if !found {
		return ""
	}
	return sb.String()
}

// systemPrompt 系统提示词（从配置读取，支持后台热更新）。
// userPrompt 为当前用户自定义追加提示词，会在系统提示词之后拼接。
func (a *Agent) systemPrompt(userPrompt string) string {
	var p string
	if a.builtin {
		p = a.cfg.Prompts.Builtin
		if p == "" {
			p = config.DefaultBuiltinPrompt
		}
		if a.schema != "" {
			p += "\n\n" + a.schema
		}
	} else {
		p = a.cfg.Prompts.Remote
		if p == "" {
			p = config.DefaultRemotePrompt
		}
	}
	if userPrompt != "" {
		p += "\n\n用户自定义要求：\n" + userPrompt
	}
	return p
}

// AskOptions 单次提问的可选覆盖项（来自 Web UI 的"基础设置"）。
// 字段为空/零值表示沿用运行配置，不覆盖。
type AskOptions struct {
	Model       string  `json:"model"`        // 覆盖模型名
	Temperature float64 `json:"temperature"`  // 覆盖生成温度（<=0 表示沿用）
	MaxTokens   int     `json:"max_tokens"`   // 覆盖单次生成上限（<=0 表示沿用）
	EnableChart *bool   `json:"enable_chart"` // 是否允许模型生成图表；nil=沿用（开启），false=关闭
	UserPrompt  string  `json:"user_prompt"`  // 用户自定义提示词追加；为空表示仅使用系统提示词
	// UserID 当前用户 ID，用于写入调用日志。
	UserID string `json:"-"`
	// ConversationID 当前会话 ID，用于写入调用日志。
	ConversationID string `json:"-"`
	// OnEvent 流式回调：处理过程中逐步推送事件（步骤/最终回答/图表/表格）。
	// 为 nil 时退化为非流式（仅返回最终 AskResult）。
	OnEvent func(StreamEvent)
}

// ShowSQLFromPrompt 从系统提示词与用户提示词中解析是否允许在前端展示 SQL。
// 用户提示词优先；若均未明确指定，默认不展示。
func ShowSQLFromPrompt(systemPrompt, userPrompt string) bool {
	lowUser := strings.ToLower(userPrompt)
	if lowUser != "" {
		if containsAny(lowUser, []string{"展示sql", "显示sql", "show sql", "输出sql"}) {
			return true
		}
		if containsAny(lowUser, []string{"不展示sql", "不显示sql", "hide sql", "不输出sql", "不要展示sql", "不要显示sql"}) {
			return false
		}
	}
	lowSys := strings.ToLower(systemPrompt)
	if containsAny(lowSys, []string{"展示sql", "显示sql", "show sql", "输出sql"}) {
		return true
	}
	if containsAny(lowSys, []string{"不展示sql", "不显示sql", "hide sql", "不输出sql", "不要展示sql", "不要显示sql"}) {
		return false
	}
	return false
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// StreamEventKind 流式事件类型。
type StreamEventKind string

const (
	EventStepStart       StreamEventKind = "step_start"        // 一次工具调用开始（含工具名/参数，工具执行期间持续展示“调用中”）
	EventStep            StreamEventKind = "step"              // 一次工具调用完成（含步骤日志）
	EventStepProgress    StreamEventKind = "step_progress"     // 工具执行期间的流式进度（如「已读取 N 行」），前端实时刷新“调用中”卡片
	EventStepResultDelta StreamEventKind = "step_result_delta" // 工具结果流式片段（让“分析过程”结果像打字机一样逐步出现）
	EventThinking        StreamEventKind = "thinking"          // LLM 思考阶段（尚未产出 token/工具调用）；调用方可据此显示“思考中…”避免像卡死
	EventAnswerDelta     StreamEventKind = "answer_delta"      // 最终回答的增量文本（逐 token 推送，实现打字机效果）
	EventAnswer          StreamEventKind = "answer"            // 最终文字结论（完整文本，流式结束时兜底/校正）
	EventResult          StreamEventKind = "result"            // 完整结构化结果（chart/rows/sql/steps），供流式前端渲染图表
	EventDone            StreamEventKind = "done"              // 整轮处理完成
	EventError           StreamEventKind = "error"             // 处理出错
)

// StreamEvent 流式处理过程中的一个事件。
type StreamEvent struct {
	Kind   StreamEventKind `json:"kind"`
	Step   *StepLog        `json:"step,omitempty"`   // EventStep / EventStepResultDelta 等步骤相关事件携带
	Text   string          `json:"text,omitempty"`   // EventAnswer 时携带最终回答
	Result *AskResult      `json:"result,omitempty"` // EventResult 时携带完整结构化结果
	Error  string          `json:"error,omitempty"`  // EventError 时携带错误信息
}

// MCPRemoteConfig 返回当前生效的远程 MCP 对接配置（供后端代理/测试接口使用）。
// 调用方通过它把浏览器侧的 MCP 请求经本服务同源转发，绕开远端服务缺失 CORS 头的问题。
func (a *Agent) MCPRemoteConfig() mcpclient.RemoteConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	// 地址智能补全（如 8081/sse、127.0.0.1:8081/sse），使代理转发也能直接使用简写配置。
	baseURL := mcpclient.NormalizeBaseURL(a.cfg.MCP.BaseURL, strings.ToLower(a.cfg.MCP.Transport) == "sse")
	return mcpclient.RemoteConfig{
		BaseURL:   baseURL,
		Transport: a.cfg.MCP.Transport,
		APIKey:    a.cfg.MCP.APIKey,
		Headers:   a.cfg.MCP.Headers,
		Timeout:   30 * time.Second,
	}
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

// UIConfig 返回当前生效的前端展示开关配置（后台可热更新）。
func (a *Agent) UIConfig() config.UIConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.UI
}

// MemoryInfo 返回当前生效的记忆窗口配置，供 server 读取历史条数上限。
func (a *Agent) MemoryInfo() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	mh := a.cfg.Agent.MemoryMaxHistory
	if mh == 0 {
		mh = 30
	}
	return map[string]int{
		"max_history": mh,
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

// AskWithStream 同 AskWith，但边处理边通过 onEvent 推送流式事件（步骤/最终回答/错误）。
// 返回最终分析文本（即使 onEvent 已消费 answer 事件，这里也兜底返回）。
// 供命令行等需要实时进度的调用方使用。
func (a *Agent) AskWithStream(question string, base *AskOptions, onEvent func(StreamEvent)) (string, error) {
	opts := &AskOptions{}
	if base != nil {
		opts.Model = base.Model
		opts.Temperature = base.Temperature
		opts.MaxTokens = base.MaxTokens
	}
	opts.OnEvent = onEvent
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

// resolveLLMClient 根据单次覆盖项返回本轮使用的 LLM 客户端。
// 无覆盖项时复用全局客户端；有覆盖项时构造一个临时客户端，不影响全局运行配置。
func (a *Agent) resolveLLMClient(opts *AskOptions) *llm.Client {
	if opts == nil {
		return a.llm
	}
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
	// 没有任何覆盖项时直接复用全局客户端，避免无谓重建。
	if model == a.cfg.LLM.Model && temp == a.cfg.LLM.Temperature && maxTok == a.cfg.LLM.MaxTokens {
		return a.llm
	}
	cli := llm.NewClient(a.cfg.LLM.Provider, a.cfg.LLM.BaseURL, model, a.cfg.LLM.APIKey, temp, maxTok)
	cli.OnLog = a.llm.OnLog
	return cli
}

// AskRichWithHistory 在带历史上下文的情况下处理一次提问，实现多轮对话记忆。
// history 为按时间正序排列的既往消息（不含本次 question），可携带结构化 extra（图表/表格/SQL）。
func (a *Agent) AskRichWithHistory(history []HistoryItem, question string, opts *AskOptions) (*AskResult, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	startTime := time.Now()

	// 若前端携带覆盖项，则本次使用一个临时 LLM 客户端，不影响全局运行配置。
	llmClient := a.resolveLLMClient(opts)

	// 图表开关：关闭时从暴露给模型的工具列表中移除图表工具（render_chart），
	// 模型便不会生成图表；最终结果的 Chart 也会在返回前清空。
	chartEnabled := opts == nil || opts.EnableChart == nil || *opts.EnableChart
	toolsForLLM := a.tools
	if !chartEnabled {
		filtered := make([]llm.Tool, 0, len(toolsForLLM))
		for _, t := range toolsForLLM {
			if t.Name == "render_chart" {
				continue
			}
			filtered = append(filtered, t)
		}
		toolsForLLM = filtered
	}

	userPrompt := ""
	if opts != nil {
		userPrompt = opts.UserPrompt
	}
	showSQL := ShowSQLFromPrompt(a.systemPrompt(userPrompt), userPrompt)

	system := a.systemPrompt(userPrompt)
	messages := []llm.Message{
		{Role: "system", Content: system},
	}

	// 记忆层：组织历史上下文（结构化回放 + 早期摘要压缩）。
	// 记忆窗口参数从运行配置读取（后台可热更新），不再固定写死。
	var summary string
	var histMsgs []llm.Message
	if len(history) > 0 {
		mc := defaultMemoryConfig()
		if a.cfg.Agent.MemorySummaryThreshold > 0 {
			mc.SummaryThreshold = a.cfg.Agent.MemorySummaryThreshold
		}
		if a.cfg.Agent.MemoryRecentKeep > 0 {
			mc.RecentKeep = a.cfg.Agent.MemoryRecentKeep
		}
		if a.cfg.Agent.MemoryMaxHistory > 0 {
			mc.MaxHistory = a.cfg.Agent.MemoryMaxHistory
		}
		summary, histMsgs = a.buildMemoryContext(history, mc, llmClient)
	}
	if summary != "" {
		memPrompt := "以下是本次对话较早阶段的记忆摘要，请结合它理解用户的连续意图：\n" + summary
		// 把记忆摘要作为一条 system 消息追加在系统提示之后、历史之前。
		messages = append(messages, llm.Message{Role: "system", Content: memPrompt})
	}
	messages = append(messages, histMsgs...)
	messages = append(messages, llm.Message{Role: "user", Content: question})
	result := &AskResult{}
	onEvent := func(ev StreamEvent) {
		if opts != nil && opts.OnEvent != nil {
			opts.OnEvent(ev)
		}
	}

	for step := 0; step < a.cfg.Agent.MaxSteps; step++ {
		var resp *llm.Response
		var err error

		// 进入 LLM 思考阶段（模型正在生成首个 token / 决策工具调用）。
		// 调用方（CLI/前端）可据此显示“思考中…”，避免迟迟无数据像卡死。
		onEvent(StreamEvent{Kind: EventThinking})

		// 逐 token 回调：若调用方提供了 OnEvent，则实时推送增量文本（打字机效果）；
		// 否则静默（非流式路径，仅返回完整结果）。
		var iterText strings.Builder
		suppress := false // 退化模式下遇到 ```json 工具块后抑制，避免泄漏到回答流
		onToken := func(delta string) {
			if delta == "" || opts == nil || opts.OnEvent == nil {
				return
			}
			if !a.cfg.Agent.UseNativeTools {
				if suppress {
					iterText.WriteString(delta)
					return
				}
				if strings.Contains(iterText.String()+delta, "```json") {
					suppress = true
					iterText.WriteString(delta)
					return
				}
			}
			iterText.WriteString(delta)
			onEvent(StreamEvent{Kind: EventAnswerDelta, Text: delta})
		}

		if a.cfg.Agent.UseNativeTools {
			logCtx := a.logContext(opts)
			resp, err = llmClient.ChatStreamWithLog(messages, toolsForLLM, onToken, logCtx)
		} else {
			logCtx := a.logContext(opts)
			resp, err = llmClient.ChatStreamWithLog(messages, nil, onToken, logCtx)
			// 退化模式：尝试从文本中解析工具调用
			if err == nil {
				resp = a.parseFallbackToolCall(resp, &messages)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("llm chat: %w", err)
		}
		if resp != nil {
			result.LLMDuration += resp.Duration.Milliseconds()
		}

		// 没有工具调用 -> 视为最终回答
		if len(resp.ToolCalls) == 0 {
			if !chartEnabled {
				result.Chart = nil // 图表关闭：确保不返回图表
			}
			if !showSQL {
				result.SQL = "" // 提示词控制：不展示 SQL 时清空返回给前端
			}
			result.Answer = strings.TrimSpace(resp.Content)
			result.TotalDuration = time.Since(startTime).Milliseconds()
			onEvent(StreamEvent{Kind: EventAnswer, Text: result.Answer})
			onEvent(StreamEvent{Kind: EventResult, Result: result})
			onEvent(StreamEvent{Kind: EventDone})
			return result, nil

		}

		// 执行所有工具调用，结果作为 tool 消息回灌
		for _, tc := range resp.ToolCalls {
			logger.Infof("[agent] 模型请求调用工具: %s 参数=%s", tc.Name, logger.Sanitize(tc.Arguments))
			// 工具调用开始：先推送“调用中”事件，让前端在工具执行期间（可能较久）有持续反馈，避免像卡死。
			onEvent(StreamEvent{Kind: EventStepStart, Step: &StepLog{Tool: tc.Name, Args: tc.Arguments}})

			// 在独立 goroutine 执行工具，主协程在等待期间周期性推送“执行中”心跳，
			// 保证前端持续有反馈（流式、不卡死）；工具自身的真实进度（如已读取行数）
			// 经 onProgress 即时推给前端。事件统一经 channel 回到主协程消费，避免并发写 SSE。
			evCh := make(chan StreamEvent, 32)
			resCh := make(chan string, 1)
			var gotRealProgress atomic.Bool
			toolStart := time.Now()
		go func() {
			userID := ""
			convID := ""
			if opts != nil {
				userID = opts.UserID
				convID = opts.ConversationID
			}
			text := a.executeTool(userID, convID, tc, result, func(message string) {
				gotRealProgress.Store(true)
				evCh <- StreamEvent{Kind: EventStepProgress, Step: &StepLog{Tool: tc.Name, Progress: message}}
			})
			resCh <- text
		}()

			ticker := time.NewTicker(900 * time.Millisecond)
			var toolResult string
		waitLoop:
			for {
				select {
				case ev := <-evCh:
					onEvent(ev)
				case text := <-resCh:
					toolResult = text
					break waitLoop
				case <-ticker.C:
					// 心跳：仅在尚无真实进度时告知前端“正在执行”，避免与真实进度文本互相覆盖。
					if !gotRealProgress.Load() {
						onEvent(StreamEvent{Kind: EventStepProgress, Step: &StepLog{Tool: tc.Name, Progress: "工具执行中…"}})
					}
				}
			}
			ticker.Stop()
			// 排空工具 goroutine 中可能残留的进度事件
		drainLoop:
			for {
				select {
				case ev := <-evCh:
					onEvent(ev)
				default:
					break drainLoop
				}
			}

			logger.Infof("[agent] 工具返回(前200): %s 耗时=%dms 结果=%s", tc.Name, time.Since(toolStart).Milliseconds(), logger.Sanitize(truncate(toolResult, 200)))
			// 展示用步骤日志：保留完整结果，前端“分析过程”不再截断。
			// 喂给 LLM 上下文的则在下方 messages 中用 truncateResult 按行数裁剪，防止上下文膨胀。
			stepLog := StepLog{Tool: tc.Name, Args: tc.Arguments, Result: toolResult, Duration: time.Since(toolStart).Milliseconds()}
			result.ToolDuration += stepLog.Duration
			result.Steps = append(result.Steps, stepLog)

			// 流式展示工具结果：把结果切成小段逐步推送，让“分析过程”动起来。
			// 大结果只流式展示前 1000 字符，避免整轮耗时过长；剩余内容在 step 完成时一并给出。
			// 短结果也至少分 2 段，确保肉眼能感知到流式效果。
			const streamChunkSize = 25
			const streamMinChunks = 2
			const maxStreamResultLen = 1000
			streamLen := min(len(toolResult), maxStreamResultLen)
			chunkSize := streamChunkSize
			if streamLen > 0 && streamLen/chunkSize < streamMinChunks {
				chunkSize = max(1, (streamLen+streamMinChunks-1)/streamMinChunks)
			}
			for i := 0; i < streamLen; i += chunkSize {
				end := min(i+chunkSize, streamLen)
				onEvent(StreamEvent{Kind: EventStepResultDelta, Step: &StepLog{Tool: tc.Name, Result: toolResult[i:end]}})
				if end < streamLen {
					time.Sleep(40 * time.Millisecond)
				}
			}
			onEvent(StreamEvent{Kind: EventStep, Step: &stepLog})
			messages = append(messages,
				llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: []llm.ToolCall{tc}},
				llm.Message{Role: "tool", Content: a.truncateResult(toolResult), ToolCallID: tc.ID, Name: tc.Name},
			)
		}
	}
	err := fmt.Errorf("已达到最大推理步数 %d，仍未给出最终结论", a.cfg.Agent.MaxSteps)
	onEvent(StreamEvent{Kind: EventError, Error: err.Error()})
	return nil, err
}

// logContext 从 AskOptions 构建 LLM 日志上下文，并注入 userID/convID 到日志条目。
// 实际回调通过闭包把 userID/convID 写入 llmLogEntry / mcpLogEntry。
func (a *Agent) logContext(opts *AskOptions) *llm.LogContext {
	if opts == nil {
		return nil
	}
	if opts.UserID == "" && opts.ConversationID == "" {
		return nil
	}
	return &llm.LogContext{UserID: opts.UserID, ConversationID: opts.ConversationID}
}

// buildLLMLogCallback 构造 LLM 调用日志回调，把请求/响应写入调用日志存储。
func (a *Agent) buildLLMLogCallback() func(logCtx *llm.LogContext, reqInfo map[string]interface{}, respText string, duration time.Duration, errMsg string) {
	return func(logCtx *llm.LogContext, reqInfo map[string]interface{}, respText string, duration time.Duration, errMsg string) {
		if a.callLogStore == nil {
			return
		}
		// 允许无用户上下文（如 CLI / 内部调用）时也记录日志，仅以空串兜底 userID/convID，
		// 确保 LLM 调用日志始终落库，不再因 logCtx 为 nil 而整条丢失。
		userID, convID := "", ""
		if logCtx != nil {
			userID, convID = logCtx.UserID, logCtx.ConversationID
		}
		_ = a.callLogStore.InsertLLMCallLog(
			userID,
			convID,
			getStr(reqInfo, "model"),
			getStr(reqInfo, "provider"),
			marshal(reqInfo["messages"]),
			marshal(reqInfo["tools"]),
			respText,
			duration.Milliseconds(),
			errMsg,
		)
	}
}

// logMCPCall 写入 MCP 工具调用日志。
func (a *Agent) logMCPCall(userID, convID, toolName, serverName, args, result string, durationMs int64, isErr bool, errMsg string) {
	if a.callLogStore == nil {
		return
	}
	_ = a.callLogStore.InsertMCPCallLog(userID, convID, toolName, serverName, args, result, durationMs, isErr, errMsg)
}

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func marshal(v interface{}) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// ---- 工具执行 ----

// executeTool 执行一个工具调用：解析参数后按工具名在注册表中查找并执行（picoclaw 风格统一分发）。
// onProgress 非 nil 时，工具执行期间的进度（如已读取行数）会经其推流给前端。
func (a *Agent) executeTool(userID, convID string, tc llm.ToolCall, result *AskResult, onProgress func(message string)) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		return fmt.Sprintf("工具参数解析失败: %v", err)
	}
	run, ok := a.toolRegistry[tc.Name]
	if !ok {
		return fmt.Sprintf("未知工具: %s", tc.Name)
	}
	toolStart := time.Now()
	text, err := run(userID, convID, args, result, onProgress)
	durationMs := time.Since(toolStart).Milliseconds()
	if err != nil {
		a.logMCPCall(userID, convID, tc.Name, a.serverNameOf(tc.Name), marshal(args), text, durationMs, false, err.Error())
		return "工具执行失败: " + err.Error()
	}
	a.logMCPCall(userID, convID, tc.Name, a.serverNameOf(tc.Name), marshal(args), text, durationMs, false, "")
	return text
}

// serverNameOf 返回提供某工具的服务名（用于 MCP 调用日志的 server_name 字段）。
// 优先查额外 MCP 路由表；主 MCP（本地/远程主服务）记为 "main"。
func (a *Agent) serverNameOf(toolName string) string {
	if a.toolRoute != nil {
		if _, ok := a.toolRoute[toolName]; ok {
			return "extra"
		}
	}
	return "main"
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
