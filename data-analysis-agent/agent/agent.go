package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"company.com/data-analysis-agent/config"
	"company.com/data-analysis-agent/internal/logger"
	"company.com/data-analysis-agent/internal/permission"
	"company.com/data-analysis-agent/llm"
	"company.com/data-analysis-agent/mcpclient"
	"company.com/data-analysis-agent/skill"
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
	tableSchemas map[string]string // 表名→结构描述缓存

	// extraMCPs 额外对接的远程 MCP 客户端（与主 MCP 并存）。
	extraMCPs []*extraMCP
	// toolRoute 工具名 -> 提供该工具的额外 MCP 客户端。主 MCP 与内置 agent 工具不在此表内。
	toolRoute map[string]*mcpclient.Client
	// toolRegistry 工具名 -> 执行函数（picoclaw 风格的统一分发表）。
	toolRegistry map[string]toolRunner

	// skills 从 skills 目录加载的技能（name -> Skill），由 LLM 通过 use_skill 工具按需加载执行。
	skills map[string]*skill.Skill

	// mu 保护 cfg/llm/mcp/tools/schema 的热更新，避免与 Ask 并发竞争。
	mu sync.RWMutex

	// callLogStore 调用日志存储（可选，HTTP 服务模式下注入 userdb）。
	callLogStore CallLogStore

	// authz 权限解析器（Agent 侧管理数据库角色权限）。
	authz *permission.Resolver
	// masker 脱敏解析器（Agent 侧管理数据脱敏）。
	masker *permission.MaskResolver

	// ragStore 知识库向量存储（RAG 增强）。
	ragStore *RAGStore
}

// CallLogStore 调用日志存储接口，由 userdb.Store 实现。
type CallLogStore interface {
	InsertLLMCallLog(userID, convID, model, provider, messages, tools, response string, durationMs int64, errorMsg string) error
	InsertMCPCallLog(userID, convID, toolName, serverName, args, result string, durationMs int64, isErr bool, errorMsg string) error
	InsertAgentActivityLog(kind, detail, target string, durationMs int64, isError bool, errMsg string) error
}

// extraMCP 一个额外远程 MCP 连接及其元信息。
type extraMCP struct {
	name   string
	client *mcpclient.Client
}

// New 构造 Agent：按配置启动 MCP（本地子进程或远程服务）、登录、预加载表结构。
func New(cfg *config.Config, callLogStore CallLogStore) (*Agent, error) {
	a := &Agent{cfg: cfg, callLogStore: callLogStore, tableSchemas: map[string]string{}}
	// 加载技能目录（失败不致命，仅告警并继续；目录为空则无技能可用）。
	skillStart := time.Now()
	a.skills, _ = skill.LoadDir(cfg.SkillsDir)
	skillDur := time.Since(skillStart).Milliseconds()
	if len(a.skills) > 0 {
		logger.Infof("[agent] 已加载 %d 个技能: %s", len(a.skills), strings.Join(skill.Names(a.skills), ", "))
		a.logActivity("skill_load", fmt.Sprintf("加载 %d 个技能: %s", len(a.skills), strings.Join(skill.Names(a.skills), ", ")), cfg.SkillsDir, skillDur, false, "")
	} else {
		logger.Infof("[agent] 未加载到技能（目录=%q，将在用户调用 use_skill 时提示无可用技能）", cfg.SkillsDir)
		a.logActivity("skill_load", "未加载到技能（目录可能为空或不存在）", cfg.SkillsDir, skillDur, false, "")
	}
	if err := a.initMCP(cfg); err != nil {
		return nil, err
	}
	a.initRAG(cfg)
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
		connStart := time.Now()
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
		connDur := time.Since(connStart).Milliseconds()
		if err != nil {
			a.logActivity("mcp_connect", "本地 MCP 子进程启动失败: "+err.Error(), cfg.MCP.ServerPath, connDur, true, err.Error())
			return err
		}
		a.logActivity("mcp_connect", "本地内置 MCP 子进程启动并建立连接", cfg.MCP.ServerPath, connDur, false, "")
	} else if remoteOn {
		// 仅远程：远程作为主 MCP（非 builtin）。
		builtin = false
		logger.Infof("[agent] 仅启用远程 MCP 对接: %s (transport=%s)", cfg.MCP.BaseURL, transportName(cfg.MCP.Transport))
		connStart := time.Now()
		mainMCP, err = mcpclient.StartRemote(mcpclient.RemoteConfig{
			BaseURL:   cfg.MCP.BaseURL,
			Transport: cfg.MCP.Transport,
			APIKey:    cfg.MCP.APIKey,
			Headers:   cfg.MCP.Headers,
			Timeout:   30 * time.Second,
		})
		connDur := time.Since(connStart).Milliseconds()
		if err != nil {
			a.logActivity("mcp_connect", "远程 MCP 连接失败: "+err.Error(), cfg.MCP.BaseURL, connDur, true, err.Error())
			return err
		}
		a.logActivity("mcp_connect", "远程 MCP 服务连接建立 (transport="+transportName(cfg.MCP.Transport)+")", cfg.MCP.BaseURL, connDur, false, "")
	} else {
		err = fmt.Errorf("MCP 未启用：请至少开启本地或远程 MCP 之一（mcp.local_enabled / mcp.remote_enabled）")
		a.logActivity("mcp_connect", err.Error(), "", 0, true, err.Error())
		return err
	}

	a.mcp = mainMCP
	a.builtin = builtin
	a.llm = llm.NewClient(cfg.LLM.Provider, cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.Temperature, cfg.LLM.MaxTokens)
	a.llm.OnLog = a.buildLLMLogCallback()

	// 内置 MCP 不再需要登录鉴权，权限由 Agent 自身管理。
	if builtin {
		logger.Infof("[agent] MCP 权限由 Agent 自身管理")
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

// initRAG 初始化知识库：加载文档、分块、计算向量。配置关闭或无需加载时静默跳过。
func (a *Agent) initRAG(cfg *config.Config) {
	if !cfg.RAG.Enabled || cfg.RAG.Source == "" {
		a.ragStore = NewRAGStore()
		logger.Infof("[rag] RAG 未启用（enabled=%v, source=%q）", cfg.RAG.Enabled, cfg.RAG.Source)
		return
	}
	store := NewRAGStore()
	chunks, err := LoadDocuments(cfg.RAG.Source, cfg.RAG.ChunkSize)
	if err != nil {
		logger.Warnf("[rag] 加载文档失败: %v", err)
		a.ragStore = store
		return
	}
	if len(chunks) == 0 {
		logger.Warnf("[rag] 未加载到任何文档（source=%q）", cfg.RAG.Source)
		a.ragStore = store
		return
	}
	embedModel := cfg.RAG.EmbeddingModel
	if embedModel == "" {
		embedModel = cfg.LLM.Model
	}
	if err := store.Add(a.llm, embedModel, chunks); err != nil {
		logger.Warnf("[rag] 计算向量失败: %v", err)
	}
	logger.Infof("[rag] 初始化完成: %d 个文档分块, 向量维度=%d", store.Len(), store.dims)
	a.ragStore = store
}

// retrieveRAGContext 检索与问题相关的知识库内容，返回格式化文本。
func (a *Agent) retrieveRAGContext(question string, embedModel string) string {
	if a.ragStore == nil || a.ragStore.Len() == 0 {
		return ""
	}
	query, err := a.llm.Embed([]string{question}, embedModel)
	if err != nil || len(query.Embeddings) == 0 {
		logger.Warnf("[rag] 检索向量失败: %v", err)
		return ""
	}
	topK := a.cfg.RAG.TopK
	if topK <= 0 {
		topK = 3
	}
	chunks := a.ragStore.Search(query.Embeddings[0], topK)
	if len(chunks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n以下是知识库中与问题相关的参考内容：\n")
	for i, c := range chunks {
		b.WriteString(fmt.Sprintf("\n[%d] ", i+1))
		if source, ok := c.Metadata["source"]; ok {
			b.WriteString(fmt.Sprintf("(来源: %s) ", source))
		}
		b.WriteString(c.Text)
		b.WriteString("\n")
	}
	b.WriteString("\n请结合以上知识库内容回答用户问题。如果知识库内容与问题无关，请忽略。")
	return b.String()
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

// InitPermissionResolver 初始化权限解析器与脱敏解析器。
// store 需同时实现 permission.PolicyStore 和 permission.MaskStore 接口。
func (a *Agent) InitPermissionResolver(store interface {
	permission.PolicyStore
	permission.MaskStore
}) {
	a.authz = permission.NewResolver(store)
	a.masker = permission.NewMaskResolver(store)
	_ = a.authz.Refresh("")
	_ = a.masker.Refresh("")
	logger.Infof("[agent] 权限解析器已初始化（Agent 侧管理数据库角色权限）")
}

// PermissionResolver 返回当前权限解析器。
// TableSchemas 返回表名→结构描述缓存。
func (a *Agent) TableSchemas() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]string, len(a.tableSchemas))
	for k, v := range a.tableSchemas {
		out[k] = v
	}
	return out
}

func (a *Agent) PermissionResolver() *permission.Resolver {
	return a.authz
}

// MaskResolver 返回当前脱敏解析器。
func (a *Agent) MaskResolver() *permission.MaskResolver {
	return a.masker
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

	// 日志开关热更新：后台修改"保存日志到文件"即时生效，无需重启。
	logger.SetSaveToFile(cfg.Log.SaveToFile)
	logger.SetDir(cfg.Log.Dir)

	// 技能目录变化：热重载技能（不重启进程即可生效）。
	if cfg.SkillsDir != a.cfg.SkillsDir {
		a.cfg.SkillsDir = cfg.SkillsDir
		if err := a.ReloadSkills(); err != nil {
			logger.Warnf("[agent] 技能热重载失败（目录=%s）: %v", cfg.SkillsDir, err)
		}
	}

	// RAG 配置变化时重新初始化知识库。
	a.cfg.RAG = cfg.RAG
	a.initRAG(cfg)
	return nil
}

// ReloadSkills 运行时热重载技能目录（无需重启进程）。
// 重新扫描 skills 目录、更新内存中的技能表，并重建暴露给大模型的工具列表（use_skill 的
// 可选枚举与描述随之更新）。失败不致命：保留上次成功加载的技能，仅记录告警与活动日志。
func (a *Agent) ReloadSkills() error {
	a.mu.Lock()
	dir := a.cfg.SkillsDir
	a.mu.Unlock()

	start := time.Now()
	skills, err := skill.LoadDir(dir)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		a.logActivity("skill_load", fmt.Sprintf("技能热重载失败（目录=%s）: %v", dir, err), dir, dur, true, err.Error())
		return err
	}
	names := skill.Names(skills)

	// 先释放写锁再重建工具列表（buildTools 内部会读 a.skills 并加读锁，避免死锁）。
	a.mu.Lock()
	a.skills = skills
	a.mu.Unlock()
	a.tools = a.buildTools()

	logger.Infof("[agent] 技能热重载完成: 目录=%s 共 %d 个: %s", dir, len(skills), strings.Join(names, ", "))
	a.logActivity("skill_load", fmt.Sprintf("技能热重载完成，共 %d 个: %s", len(skills), strings.Join(names, ", ")), dir, dur, false, "")
	return nil
}

// RAGInfo 返回知识库状态信息。
func (a *Agent) RAGInfo() map[string]interface{} {
	a.mu.RLock()
	defer a.mu.RUnlock()
	info := map[string]interface{}{
		"enabled": a.cfg.RAG.Enabled,
		"source":  a.cfg.RAG.Source,
		"chunks":  0,
		"dims":    0,
		"status":  "未初始化",
	}
	if a.ragStore != nil {
		info["chunks"] = a.ragStore.Len()
		info["dims"] = a.ragStore.Dims()
		if a.ragStore.Len() > 0 {
			info["status"] = "就绪"
		} else {
			info["status"] = "空"
		}
	}
	return info
}

// ReloadRAG 重新加载知识库文档并重建向量索引。
func (a *Agent) ReloadRAG() error {
	a.mu.Lock()
	cfg := *a.cfg
	a.mu.Unlock()
	a.initRAG(&cfg)
	info := a.RAGInfo()
	logger.Infof("[rag] 知识库重新加载完成: chunks=%d dims=%d", info["chunks"], info["dims"])
	return nil
}

// UploadRAGDocument 上传一个文档文件到知识库源目录并重建索引。
func (a *Agent) UploadRAGDocument(filename string, data []byte) error {
	a.mu.RLock()
	source := a.cfg.RAG.Source
	a.mu.RUnlock()
	if source == "" {
		return fmt.Errorf("知识库源路径未配置，请先在系统配置中设置 rag.source")
	}
	// 如果 source 是文件，将上传文件放到同目录
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("知识库源路径无效: %v", err)
	}
	dir := source
	if !info.IsDir() {
		dir = filepath.Dir(source)
	}
	dest := filepath.Join(dir, filename)
	if err := os.WriteFile(dest, data, 0644); err != nil {
		return fmt.Errorf("保存文件失败: %v", err)
	}
	logger.Infof("[rag] 上传文档: %s -> %s", filename, dest)
	return a.ReloadRAG()
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
// （用于"本地+远程同时开启"场景：本地是主 MCP，远程作为额外 MCP 聚合工具）。
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
	// 技能：若加载了技能，则向 LLM 暴露 use_skill 工具，由模型按需加载并据其指引执行。
	// use_skill 与既有 MCP 工具并列，不抢占任何工具名（除非用户自定义了同名技能/工具，以先注册者为准）。
	a.mu.RLock()
	hasSkills := len(a.skills) > 0
	skillNames := skill.Names(a.skills)
	skillDesc := skill.ToolDescription(a.skills)
	a.mu.RUnlock()
	if hasSkills {
		tools = append(tools, llm.Tool{
			Name:        "use_skill",
			Description: skillDesc,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "要加载的技能名称（必须来自下方可用技能列表）",
						"enum":        skillNames,
					},
				},
				"required": []string{"name"},
			},
		})
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
		schemaStart := time.Now()
		params := map[string]interface{}{"table": t}
		text, isErr, err := a.mcp.CallTool("describe_table", params, nil)
		schemaDur := time.Since(schemaStart).Milliseconds()
		// 初始化阶段的 MCP 调用也写入 mcp_call_logs，便于追溯首次表结构获取；
		// 以 user_id="system" 标记，与正常对话调用区分（convID 为空）。
		a.logMCPCall("system", "", "describe_table", "main",
			fmt.Sprintf("{\"token\":\"***\",\"table\":\"%s\"}", t), text, schemaDur, isErr, "")
		if err != nil {
			a.logActivity("schema_load", fmt.Sprintf("获取表 %s 结构失败: %v", t, err), t, schemaDur, true, err.Error())
			continue
		}
		if isErr || text == "" {
			a.logActivity("schema_load", fmt.Sprintf("获取表 %s 结构返回空或错误", t), t, schemaDur, true, "empty or error")
			continue
		}
		sb.WriteString("- " + t + ": " + text + "\n")
		found = true
		a.tableSchemas[t] = text
		a.logActivity("schema_load", fmt.Sprintf("获取表 %s 结构成功: %s", t, text), t, schemaDur, false, "")
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

// parsePlan 解析 LLM 输出的计划文本为步骤列表。
func (a *Agent) parsePlan(text string) []string {
	var steps []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// 匹配 "1. xxx" "1、xxx" "- xxx" "* xxx"
		if len(trimmed) > 2 && (trimmed[0] == '-' || trimmed[0] == '*') {
			steps = append(steps, strings.TrimSpace(trimmed[1:]))
		} else if len(trimmed) > 3 && trimmed[0] >= '1' && trimmed[0] <= '9' && strings.ContainsAny(trimmed[1:3], ".、") {
			steps = append(steps, strings.TrimSpace(trimmed[2:]))
		}
	}
	if len(steps) == 0 {
		steps = []string{text}
	}
	return steps
}

// AskOptions 单次提问的可选覆盖项（来自 Web UI 的"基础设置"）。
// 字段为空/零值表示沿用运行配置，不覆盖。
type AskOptions struct {
	Model       string  `json:"model"`        // 覆盖模型名
	Temperature float64 `json:"temperature"`  // 覆盖生成温度（<=0 表示沿用）
	MaxTokens   int     `json:"max_tokens"`   // 覆盖单次生成上限（<=0 表示沿用）
	EnableChart *bool   `json:"enable_chart"` // 是否允许模型生成图表；nil=沿用（开启），false=关闭
	UserPrompt  string  `json:"user_prompt"`  // 用户自定义提示词追加；为空表示仅使用系统提示词
	Mode        string  `json:"mode"`         // 覆盖运行模式：react | plan；为空表示沿用运行配置
	// PlanOnly 仅生成计划不执行：为 true 时生成计划后 emit EventPlan + EventDone 并停止，不进入 ReAct 循环。
	PlanOnly bool `json:"plan_only"`
	// SelectedSteps 用户从计划中勾选的步骤文本列表（仅 plan 模式下有效）。
	// 非空时跳过计划生成，直接用这些步骤作为执行上下文运行 ReAct 循环。
	SelectedSteps []string `json:"selected_steps"`
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
	EventStepStart       StreamEventKind = "step_start"        // 一次工具调用开始（含工具名/参数，工具执行期间持续展示"调用中"）
	EventStep            StreamEventKind = "step"              // 一次工具调用完成（含步骤日志）
	EventStepProgress    StreamEventKind = "step_progress"     // 工具执行期间的流式进度（如「已读取 N 行」），前端实时刷新"调用中"卡片
	EventStepResultDelta StreamEventKind = "step_result_delta" // 工具结果流式片段（让"分析过程"结果像打字机一样逐步出现）
	EventPlan            StreamEventKind = "plan"              // plan 模式下生成的计划步骤列表
	EventThinking        StreamEventKind = "thinking"          // LLM 思考阶段（尚未产出 token/工具调用）；调用方可据此显示"思考中…"避免像卡死
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
	Plan   []string        `json:"plan,omitempty"`   // EventPlan 时携带计划步骤列表
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

// ListModels 从 LLM 提供商获取可用模型列表。
func (a *Agent) ListModels() ([]string, error) {
	cli := llm.NewClient(a.cfg.LLM.Provider, a.cfg.LLM.BaseURL, a.cfg.LLM.Model, a.cfg.LLM.APIKey, 0, 0)
	return cli.ListModels()
}

// UIConfig 返回当前生效的前端展示开关配置（后台可热更新）。
func (a *Agent) UIConfig() config.UIConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.UI
}

// SkillNames 返回已加载技能的名称列表（按字典序），供 REPL /skill 命令与接口展示。
func (a *Agent) SkillNames() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return skill.Names(a.skills)
}

// SkillDescription 返回指定技能的描述（无该技能时返回空串）。
func (a *Agent) SkillDescription(name string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if s, ok := a.skills[name]; ok {
		return s.Description
	}
	return ""
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

// ConvCompressInfo 返回对话压缩为 skill 的配置，供 server 判断何时触发。
func (a *Agent) ConvCompressInfo() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	threshold := a.cfg.Agent.ConversationCompressTurns
	if threshold < 0 {
		threshold = 0
	}
	return map[string]int{
		"compress_turns": threshold,
	}
}

// SkillsDir 返回当前技能目录路径。
func (a *Agent) SkillsDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.SkillsDir
}

// SkillContent 返回已加载技能的元数据与正文副本；name 不存在时返回 nil。
func (a *Agent) SkillContent(name string) *skill.Skill {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, ok := a.skills[name]
	if !ok || s == nil {
		return nil
	}
	cp := *s
	return &cp
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

	// RAG 知识库检索：若启用则将相关文档分块注入系统提示。
	if a.cfg.RAG.Enabled && a.ragStore != nil && a.ragStore.Len() > 0 {
		embedModel := a.cfg.RAG.EmbeddingModel
		if embedModel == "" {
			embedModel = a.cfg.LLM.Model
		}
		ragCtx := a.retrieveRAGContext(question, embedModel)
		if ragCtx != "" {
			system += ragCtx
		}
	}

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

	// Plan 模式：先生成计划，再执行。
	agentMode := a.cfg.Agent.Mode
	if opts != nil && opts.Mode != "" {
		agentMode = opts.Mode
	}
	if agentMode == "plan" {
		// 用户已确认计划（带 selected_steps）：跳过计划生成，直接用选中步骤作为执行上下文。
		if opts != nil && len(opts.SelectedSteps) > 0 {
			var planText string
			for i, s := range opts.SelectedSteps {
				planText += fmt.Sprintf("%d. %s\n", i+1, s)
			}
			planSteps := opts.SelectedSteps
			onEvent(StreamEvent{Kind: EventPlan, Plan: planSteps})
			messages = append(messages, llm.Message{Role: "assistant", Content: "## 分析计划\n\n" + planText})
			messages = append(messages, llm.Message{Role: "user", Content: "请按照以上计划逐步执行。每完成一步，用工具获取数据后再进行下一步。所有步骤完成后给出最终分析结论。"})
		} else {
			// 生成计划：使用自定义提示词或默认提示词。
			planSuffix := a.cfg.Agent.PlanPrompt
			if planSuffix == "" {
				planSuffix = "请先制定一个详细的分析计划，列出3-5个具体步骤。\n仅输出计划，不要执行工具。"
			}
			planMsgs := []llm.Message{
				{Role: "system", Content: system + "\n\n" + planSuffix},
				{Role: "user", Content: question},
			}
			planResp, err := llmClient.Chat(planMsgs, nil)
			if err == nil && planResp != nil {
				planText := strings.TrimSpace(planResp.Content)
				planSteps := a.parsePlan(planText)
				onEvent(StreamEvent{Kind: EventPlan, Plan: planSteps})

				// plan_only 模式：仅生成计划，不执行，返回。
				if opts != nil && opts.PlanOnly {
					result.Answer = planText
					onEvent(StreamEvent{Kind: EventAnswer, Text: planText})
					onEvent(StreamEvent{Kind: EventResult, Result: result})
					onEvent(StreamEvent{Kind: EventDone})
					return result, nil
				}

				// plan_auto_execute 或默认：将计划注入上下文并继续执行。
				messages = append(messages, llm.Message{Role: "assistant", Content: "## 分析计划\n\n" + planText})
				messages = append(messages, llm.Message{Role: "user", Content: "请按照以上计划逐步执行。每完成一步，用工具获取数据后再进行下一步。所有步骤完成后给出最终分析结论。"})
			}
			// 即使 plan 生成失败也继续 ReAct 循环（退化到普通模式）
		}
	}

	for step := 0; step < a.cfg.Agent.MaxSteps; step++ {
		var resp *llm.Response
		var err error

		// 进入 LLM 思考阶段（模型正在生成首个 token / 决策工具调用）。
		// 调用方（CLI/前端）可据此显示"思考中…"，避免迟迟无数据像卡死。
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
			answer := strings.TrimSpace(resp.Content)
			// 如果 LLM 返回空内容且还有剩余步数，跳过本轮继续（可能是 LLM 输出异常）
			if answer == "" && step < a.cfg.Agent.MaxSteps-1 {
				messages = append(messages, llm.Message{Role: "assistant", Content: ""})
				messages = append(messages, llm.Message{Role: "user", Content: "请继续分析，如果需要工具请直接调用，如果有结论请直接输出。"})
				continue
			}
			if !chartEnabled {
				result.Chart = nil
			}
			if !showSQL {
				result.SQL = ""
			}
			result.Answer = answer
			result.TotalDuration = time.Since(startTime).Milliseconds()
			onEvent(StreamEvent{Kind: EventAnswer, Text: result.Answer})
			onEvent(StreamEvent{Kind: EventResult, Result: result})
			onEvent(StreamEvent{Kind: EventDone})
			return result, nil

		}

		// 执行所有工具调用，结果作为 tool 消息回灌。
		// 并发上限由 agent.tool_concurrency 控制：<=1 串行（默认，零风险）；>1 并发执行后统一回灌。
		if a.cfg.Agent.ToolConcurrency <= 1 {
			for _, tc := range resp.ToolCalls {
				logger.Infof("[agent] 模型请求调用工具: %s 参数=%s", tc.Name, logger.Sanitize(tc.Arguments))
				// 工具调用开始：先推送"调用中"事件，让前端在工具执行期间（可能较久）有持续反馈，避免像卡死。
				onEvent(StreamEvent{Kind: EventStepStart, Step: &StepLog{ID: tc.ID, Tool: tc.Name, Args: tc.Arguments}})

				// 在独立 goroutine 执行工具，主协程在等待期间周期性推送"执行中"心跳，
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
					evCh <- StreamEvent{Kind: EventStepProgress, Step: &StepLog{ID: tc.ID, Tool: tc.Name, Progress: message}}
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
						// 心跳：仅在尚无真实进度时告知前端"正在执行"，避免与真实进度文本互相覆盖。
						if !gotRealProgress.Load() {
							onEvent(StreamEvent{Kind: EventStepProgress, Step: &StepLog{ID: tc.ID, Tool: tc.Name, Progress: "工具执行中…"}})
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

				logger.Infof("[agent] 工具返回(前%d): %s 耗时=%dms 结果=%s", a.cfg.Agent.LogPreviewChars, tc.Name, time.Since(toolStart).Milliseconds(), logger.Sanitize(truncate(toolResult, a.cfg.Agent.LogPreviewChars)))
				// 展示用步骤日志：保留完整结果，前端"分析过程"不再截断。
				// 喂给 LLM 上下文的则在下方 messages 中用 truncateResult 按行数裁剪，防止上下文膨胀。
				stepLog := StepLog{ID: tc.ID, Tool: tc.Name, Args: tc.Arguments, Result: toolResult, Duration: time.Since(toolStart).Milliseconds()}
				result.ToolDuration += stepLog.Duration
				result.Steps = append(result.Steps, stepLog)

				// 流式展示工具结果：把结果切成小段逐步推送，让"分析过程"动起来。
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
					onEvent(StreamEvent{Kind: EventStepResultDelta, Step: &StepLog{ID: tc.ID, Tool: tc.Name, Result: toolResult[i:end]}})
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
		} else {
			a.runToolsConcurrent(resp, opts, result, onEvent, &messages)
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

// logActivity 写入 Agent 内部活动日志（初始化阶段或运行期关键内部行为，
// 如 MCP 连接建立、技能加载、初始化预加载表结构等，不经过 HTTP 中间件也不属于单轮对话）。
func (a *Agent) logActivity(kind, detail, target string, durationMs int64, isError bool, errMsg string) {
	if a.callLogStore == nil {
		return
	}
	_ = a.callLogStore.InsertAgentActivityLog(kind, detail, target, durationMs, isError, errMsg)
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

// toolOutcome 并发执行时单个工具调用的独立结果，避免多 goroutine 共享写入同一 AskResult 产生数据竞争。
type toolOutcome struct {
	tc      llm.ToolCall
	shadow  *AskResult // 该工具独立写入的结构化结果（SQL/Chart/Rows），与主 result 隔离
	text    string     // 工具返回文本（完整，前端展示用）
	stepLog StepLog
}

// runToolsConcurrent 并发执行同一轮 LLM 返回的多个工具调用（agent.tool_concurrency>1 时启用）。
// 设计要点：
//   - 每个工具在独立 goroutine 执行，写入各自的 shadow AskResult，彻底避免对主 result 的并发写竞争。
//   - 所有进度/结果流式事件都带 tc.ID，并经同一个 evCh 汇总到主协程单点写出 SSE；
//     前端按 ID 归类到独立卡片，并发时多张卡片各自更新，输出不混乱。
//   - WaitGroup 等待全部完成后，按原始 tool_calls 顺序合并 shadow 到主 result 并回灌 messages（tool_call_id 一一对应）。
func (a *Agent) runToolsConcurrent(resp *llm.Response, opts *AskOptions, result *AskResult, onEvent func(StreamEvent), messages *[]llm.Message) {
	n := len(resp.ToolCalls)
	// 先为每个工具推送 step_start，前端据此建立独立卡片（按 ID 归类），并发期间多张卡片各自更新。
	for _, tc := range resp.ToolCalls {
		onEvent(StreamEvent{Kind: EventStepStart, Step: &StepLog{ID: tc.ID, Tool: tc.Name, Args: tc.Arguments}})
	}

	evCh := make(chan StreamEvent, 64)
	outcomes := make([]toolOutcome, n)

	userID, convID := "", ""
	if opts != nil {
		userID, convID = opts.UserID, opts.ConversationID
	}

	var wg sync.WaitGroup
	for i, tc := range resp.ToolCalls {
		wg.Add(1)
		go func(i int, tc llm.ToolCall) {
			defer wg.Done()
			shadow := &AskResult{}
			toolStart := time.Now()
			text := a.executeTool(userID, convID, tc, shadow, func(message string) {
				evCh <- StreamEvent{Kind: EventStepProgress, Step: &StepLog{ID: tc.ID, Tool: tc.Name, Progress: message}}
			})
			dur := time.Since(toolStart).Milliseconds()
			// 流式展示工具结果：切片逐步推送（带 ID，前端归类），与串行行为一致。
			const streamChunkSize = 25
			const streamMinChunks = 2
			const maxStreamResultLen = 1000
			streamLen := min(len(text), maxStreamResultLen)
			chunkSize := streamChunkSize
			if streamLen > 0 && streamLen/chunkSize < streamMinChunks {
				chunkSize = max(1, (streamLen+streamMinChunks-1)/streamMinChunks)
			}
			for j := 0; j < streamLen; j += chunkSize {
				end := min(j+chunkSize, streamLen)
				evCh <- StreamEvent{Kind: EventStepResultDelta, Step: &StepLog{ID: tc.ID, Tool: tc.Name, Result: text[j:end]}}
				if end < streamLen {
					time.Sleep(40 * time.Millisecond)
				}
			}
			stepLog := StepLog{ID: tc.ID, Tool: tc.Name, Args: tc.Arguments, Result: text, Duration: dur}
			outcomes[i] = toolOutcome{tc: tc, shadow: shadow, text: text, stepLog: stepLog}
			evCh <- StreamEvent{Kind: EventStep, Step: &stepLog}
		}(i, tc)
	}

	// 主协程：单点消费事件并写出 SSE，直到所有工具完成。
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	for {
		select {
		case ev := <-evCh:
			onEvent(ev)
		case <-done:
		drain:
			for {
				select {
				case ev := <-evCh:
					onEvent(ev)
				default:
					break drain
				}
			}
			a.mergeToolOutcomes(resp, outcomes, result, messages)
			return
		}
	}
}

// mergeToolOutcomes 并发完成后按原始顺序合并各工具的独立结果到主 result，并回灌 messages。
func (a *Agent) mergeToolOutcomes(resp *llm.Response, outcomes []toolOutcome, result *AskResult, messages *[]llm.Message) {
	// 一个 assistant 消息承载本轮全部 tool_calls，其后为每个工具对应的 tool 消息（按 tool_call_id 匹配）。
	*messages = append(*messages,
		llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls},
	)
	for _, o := range outcomes {
		// 合并结构化结果：SQL 取最后一个非空；Chart 保持"已存在不覆盖"语义；Rows 取最后一个非空（与串行"后者覆盖"一致）。
		if o.shadow.SQL != "" {
			result.SQL = o.shadow.SQL
		}
		if o.shadow.Chart != nil && result.Chart == nil {
			result.Chart = o.shadow.Chart
		}
		if len(o.shadow.Rows) > 0 {
			result.Rows = o.shadow.Rows
		}
		result.ToolDuration += o.stepLog.Duration
		result.Steps = append(result.Steps, o.stepLog)
		*messages = append(*messages,
			llm.Message{Role: "tool", Content: a.truncateResult(o.text), ToolCallID: o.tc.ID, Name: o.tc.Name},
		)
	}
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
	return truncate(text, a.cfg.Agent.MaxResultChars)
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

// truncate 文本截断辅助。n<=0 表示不截断（返回原文）。
func truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("... (已截断，共 %d 字符)", len(r))
}
