// Package confdb 把数据分析助手的运行配置（LLM / MCP / Agent / 提示词）持久化到 SQLite，
// 并缓存到内存，支持后台管理页面在不重启进程的情况下热更新配置。
//
// 首次运行时，若数据库为空，则从 config.json（或内置默认值）播种；之后以数据库为准。
package confdb

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"company.com/data-analysis-agent/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ConfigItem 数据库中的一条配置记录（键值 + JSON 字符串值）。
type ConfigItem struct {
	Key         string    `gorm:"primaryKey;size:128" json:"key"`
	Value       string    `gorm:"type:text" json:"value"`
	Description string    `gorm:"size:255" json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 显式指定表名。
func (ConfigItem) TableName() string { return "app_config" }

// Store 配置存储：内存缓存 + SQLite 持久化。
type Store struct {
	db   *gorm.DB
	mu   sync.RWMutex
	cfg  *config.Config
	path string
}

// New 打开（或创建）SQLite 数据库，自动建表，并按需播种初始配置。
// fileConfig 为 config.json 加载后的配置（可为 nil，表示仅用内置默认值）。
func New(dbPath string, fileConfig *config.Config) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("confdb path is empty")
	}
	db, err := gorm.Open(sqlite.Open(dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open config db %s: %w", dbPath, err)
	}
	if err := configurePool(db); err != nil {
		return nil, fmt.Errorf("configure config db pool: %w", err)
	}
	if err := db.AutoMigrate(&ConfigItem{}); err != nil {
		return nil, fmt.Errorf("migrate config db: %w", err)
	}
	s := &Store{db: db, path: dbPath}
	if err := s.seedIfEmpty(fileConfig); err != nil {
		return nil, err
	}
	cfg, err := s.load()
	if err != nil {
		return nil, err
	}
	s.cfg = cfg
	return s, nil
}

// configurePool 配置 SQLite 连接池，避免并发访问冲突。
func configurePool(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(1 * time.Hour)
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		log.Printf("warn: confdb WAL mode: %v", err)
	}
	if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil {
		log.Printf("warn: confdb busy_timeout: %v", err)
	}
	return nil
}

// DBPath 返回数据库文件路径。
func (s *Store) DBPath() string { return s.path }

// AdminCreds 返回后台管理登录凭据（来自数据库配置）。
func (s *Store) AdminCreds() (username, password string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items, err := s.List()
	if err != nil {
		log.Printf("warn: load admin creds: %v", err)
		return "admin", "admin123"
	}
	for _, it := range items {
		switch it.Key {
		case KeyAdminUser:
			username = it.Value
		case KeyAdminPass:
			password = it.Value
		}
	}
	if username == "" {
		username = "admin"
	}
	if password == "" {
		password = "admin123"
	}
	return username, password
}

// seedIfEmpty 仅在数据库为空时写入初始配置（文件配置优先，否则用内置默认值）。
func (s *Store) seedIfEmpty(fileConfig *config.Config) error {
	var cnt int64
	if err := s.db.Model(&ConfigItem{}).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return nil
	}
	seed := fileConfig
	if seed == nil {
		seed = config.DefaultConfig()
	}
	items := toItems(seed)
	return s.db.Create(&items).Error
}

// load 从数据库读取全部配置项，组装为完整 *config.Config。
func (s *Store) load() (*config.Config, error) {
	var items []ConfigItem
	if err := s.db.Order("key").Find(&items).Error; err != nil {
		return nil, err
	}
	cfg := config.DefaultConfig()
	for _, it := range items {
		if err := applyItem(cfg, it.Key, it.Value); err != nil {
			return nil, fmt.Errorf("apply config %s: %w", it.Key, err)
		}
	}
	return cfg, nil
}

// Get 返回当前生效配置（内存缓存，读锁安全）。
func (s *Store) Get() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 返回副本，避免调用方修改内部状态。
	cp := *s.cfg
	return &cp
}

// List 返回全部配置项（供后台页面展示）。
func (s *Store) List() ([]ConfigItem, error) {
	var items []ConfigItem
	if err := s.db.Order("key").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// Update 批量更新若干配置项（key->value），持久化到数据库并刷新内存缓存。
// 仅接受已知 key，未知 key 返回错误。
func (s *Store) Update(patch map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.db.Begin()
	for k, v := range patch {
		if !validKey(k) {
			tx.Rollback()
			return fmt.Errorf("未知配置项: %s", k)
		}
		item := ConfigItem{Key: k, Value: v, UpdatedAt: time.Now()}
		if err := tx.Save(&item).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	cfg, err := s.load()
	if err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

// Reset 清空所有配置并重置为内置默认值（factory reset）。
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.db.Where("1 = 1").Delete(&ConfigItem{}).Error; err != nil {
		return err
	}
	seed := config.DefaultConfig()
	items := toItems(seed)
	if err := s.db.Create(&items).Error; err != nil {
		return err
	}
	cfg, err := s.load()
	if err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

// ---- 配置项键定义与读写 ----

// 配置项键常量（后台页面与 API 共用）。
const (
	// LLM
	KeyLLMProvider    = "llm.provider"
	KeyLLMBaseURL     = "llm.base_url"
	KeyLLMModel       = "llm.model"
	KeyLLMAPIKey      = "llm.api_key"
	KeyLLMTemperature = "llm.temperature"
	KeyLLMMaxTokens   = "llm.max_tokens"
	// MCP（local）
	KeyMCPMode       = "mcp.mode"
	KeyMCPServerPath = "mcp.server_path"
	KeyMCPDBDialect  = "mcp.db_dialect"
	KeyMCPDBDSN      = "mcp.db_dsn"
	KeyMCPMask       = "mcp.mask_enabled"
	KeyMCPSeed       = "mcp.seed_demo"
	KeyMCPSandbox    = "mcp.sandbox_enabled"
	KeyMCPWorkDir    = "mcp.work_dir"
	KeyMCPUsername   = "mcp.username"
	KeyMCPPassword   = "mcp.password"
	// MCP（remote）
	KeyMCPBaseURL   = "mcp.base_url"
	KeyMCPTransport = "mcp.transport"
	KeyMCPAPIKey    = "mcp.api_key"
	// MCP（额外对接的远程服务列表，JSON 数组）
	KeyMCPExtra = "mcp.extra"
	// Agent
	KeyAgentMaxSteps      = "agent.max_steps"
	KeyAgentUseNative     = "agent.use_native_tools"
	KeyAgentMaxResultRows = "agent.max_result_rows"
	// Agent 记忆窗口
	KeyAgentMemMaxHistory       = "agent.memory_max_history"
	KeyAgentMemSummaryThreshold = "agent.memory_summary_threshold"
	KeyAgentMemRecentKeep       = "agent.memory_recent_keep"
	// Log
	KeyLogSaveToFile = "log.save_to_file"
	KeyLogDir        = "log.dir"
	// Prompts
	KeyPromptBuiltin = "prompts.builtin"
	KeyPromptRemote  = "prompts.remote"
	// UI 展示开关
	KeyUIShowDuration = "ui.show_duration"
	KeyUIShowSteps    = "ui.show_steps"
	KeyUIShowImages   = "ui.show_images"
	KeyUITheme        = "ui.theme"
	KeyUIAppTitle     = "ui.app_title"
	KeyUIAppSubtitle  = "ui.app_subtitle"
	KeyUIWorkflow     = "ui.workflow_steps"
	KeyUIAdminPageSize = "ui.admin_page_size"
	KeyUIChatPageSize  = "ui.chat_page_size"
	KeyUIPhoneRequired = "ui.phone_required"
	KeyUIPhoneVerify   = "ui.phone_verify_required"
	// Admin 后台登录凭据
	KeyAdminUser = "admin.username"
	KeyAdminPass = "admin.password"
)

func validKey(k string) bool {
	switch k {
	case KeyLLMProvider, KeyLLMBaseURL, KeyLLMModel, KeyLLMAPIKey, KeyLLMTemperature, KeyLLMMaxTokens,
		KeyMCPMode, KeyMCPServerPath, KeyMCPDBDialect, KeyMCPDBDSN, KeyMCPMask, KeyMCPSeed,
		KeyMCPSandbox, KeyMCPWorkDir,
		KeyMCPUsername, KeyMCPPassword, KeyMCPBaseURL, KeyMCPTransport, KeyMCPAPIKey, KeyMCPExtra,
		KeyAgentMaxSteps, KeyAgentUseNative, KeyAgentMaxResultRows,
		KeyAgentMemMaxHistory, KeyAgentMemSummaryThreshold, KeyAgentMemRecentKeep,
		KeyLogSaveToFile, KeyLogDir,
		KeyPromptBuiltin, KeyPromptRemote,
		KeyUIShowDuration, KeyUIShowSteps, KeyUIShowImages,
		KeyUITheme, KeyUIAppTitle, KeyUIAppSubtitle, KeyUIWorkflow,
		KeyUIAdminPageSize, KeyUIChatPageSize, KeyUIPhoneRequired, KeyUIPhoneVerify,
		KeyAdminUser, KeyAdminPass:
		return true
	}
	return false
}

// toItems 将配置结构展开为数据库记录列表。
func toItems(c *config.Config) []ConfigItem {
	now := time.Now()
	mk := func(key, value, desc string) ConfigItem {
		return ConfigItem{Key: key, Value: value, Description: desc, UpdatedAt: now}
	}
	return []ConfigItem{
		mk(KeyLLMProvider, c.LLM.Provider, "LLM 提供方：ollama | openai"),
		mk(KeyLLMBaseURL, c.LLM.BaseURL, "LLM 服务地址"),
		mk(KeyLLMModel, c.LLM.Model, "模型名"),
		mk(KeyLLMAPIKey, c.LLM.APIKey, "API Key（openai 兼容需要）"),
		mk(KeyLLMTemperature, f64(c.LLM.Temperature), "生成温度"),
		mk(KeyLLMMaxTokens, itoa(c.LLM.MaxTokens), "单次生成最大 token"),
		mk(KeyMCPMode, c.MCP.Mode, "MCP 对接模式：local | remote"),
		mk(KeyMCPServerPath, c.MCP.ServerPath, "本地 mcp-data-server 可执行文件路径"),
		mk(KeyMCPDBDialect, c.MCP.DBDialect, "后端数据库类型：sqlite | mysql"),
		mk(KeyMCPDBDSN, c.MCP.DBDsn, "后端数据库连接串"),
		mk(KeyMCPMask, b(c.MCP.MaskEnabled), "是否启用脱敏"),
		mk(KeyMCPSeed, b(c.MCP.SeedDemo), "是否写入演示数据"),
		mk(KeyMCPSandbox, b(c.MCP.SandboxEnabled), "是否启用工作目录沙箱（true=文件工具只能访问 work_dir 内；false=系统环境模式，可访问任意绝对路径）"),
		mk(KeyMCPWorkDir, c.MCP.WorkDir, "文件工具根目录（沙箱模式限定在此；留空=进程工作目录）"),
		mk(KeyMCPUsername, c.MCP.Username, "MCP 登录账号"),
		mk(KeyMCPPassword, c.MCP.Password, "MCP 登录密码"),
		mk(KeyMCPBaseURL, c.MCP.BaseURL, "远程 MCP 地址（remote 模式）"),
		mk(KeyMCPTransport, c.MCP.Transport, "远程传输方式：streamable-http | sse"),
		mk(KeyMCPAPIKey, c.MCP.APIKey, "远程 MCP 鉴权 Key"),
		mk(KeyMCPExtra, marshalExtra(c.MCP.Extra), "额外对接的远程 MCP 服务列表（JSON 数组）"),
		mk(KeyAgentMaxSteps, itoa(c.Agent.MaxSteps), "ReAct 最大推理步数"),
		mk(KeyAgentUseNative, b(c.Agent.UseNativeTools), "是否使用原生工具调用"),
		mk(KeyAgentMaxResultRows, itoa(c.Agent.MaxResultRows), "工具返回最大行数"),
		mk(KeyAgentMemMaxHistory, itoa(c.Agent.MemoryMaxHistory), "上下文窗口：单次最多回放的历史消息条数"),
		mk(KeyAgentMemSummaryThreshold, itoa(c.Agent.MemorySummaryThreshold), "历史消息数达到该值时触发摘要压缩"),
		mk(KeyAgentMemRecentKeep, itoa(c.Agent.MemoryRecentKeep), "摘要压缩时保留最近 N 条原文"),
		mk(KeyLogSaveToFile, b(c.Log.SaveToFile), "是否把每个环节的请求日志保存到文件"),
		mk(KeyLogDir, c.Log.Dir, "日志文件目录（默认 logs）"),
		mk(KeyPromptBuiltin, c.Prompts.Builtin, "内置数据库分析场景系统提示词"),
		mk(KeyPromptRemote, c.Prompts.Remote, "远程 MCP 场景系统提示词"),
		mk(KeyUIShowDuration, b(c.UI.ShowDuration), "是否展示耗时统计"),
		mk(KeyUIShowSteps, b(c.UI.ShowSteps), "是否展示分析过程"),
		mk(KeyUIShowImages, b(c.UI.ShowImages), "是否展示图片/图表"),
		mk(KeyUITheme, c.UI.Theme, "后台管理页面主题：dark | light | auto"),
		mk(KeyUIAppTitle, c.UI.AppTitle, "应用标题（前后台共用）"),
		mk(KeyUIAppSubtitle, c.UI.AppSubtitle, "应用副标题/描述（前后台共用）"),
		mk(KeyUIWorkflow, c.UI.WorkflowSteps, "前台顶部流程步骤文案，用 → 分隔"),
		mk(KeyUIAdminPageSize, itoa(c.UI.AdminPageSize), "后台管理页面默认分页大小"),
		mk(KeyUIChatPageSize, itoa(c.UI.ChatPageSize), "前端聊天消息分页默认大小"),
		mk(KeyUIPhoneRequired, b(c.UI.PhoneRequired), "注册是否强制填写手机号"),
		mk(KeyUIPhoneVerify, b(c.UI.PhoneVerifyRequired), "是否强制手机号验证（当前格式校验）"),
		mk(KeyAdminUser, "admin", "后台管理登录账号"),
		mk(KeyAdminPass, "admin123", "后台管理登录密码"),
	}
}

// applyItem 把单个键值写入配置结构对应字段。
func applyItem(c *config.Config, key, value string) error {
	switch key {
	case KeyLLMProvider:
		c.LLM.Provider = value
	case KeyLLMBaseURL:
		c.LLM.BaseURL = value
	case KeyLLMModel:
		c.LLM.Model = value
	case KeyLLMAPIKey:
		c.LLM.APIKey = value
	case KeyLLMTemperature:
		c.LLM.Temperature = atof(value)
	case KeyLLMMaxTokens:
		c.LLM.MaxTokens = atoi(value)
	case KeyMCPMode:
		c.MCP.Mode = value
	case KeyMCPServerPath:
		c.MCP.ServerPath = value
	case KeyMCPDBDialect:
		c.MCP.DBDialect = value
	case KeyMCPDBDSN:
		c.MCP.DBDsn = value
	case KeyMCPMask:
		c.MCP.MaskEnabled = isTrue(value)
	case KeyMCPSeed:
		c.MCP.SeedDemo = isTrue(value)
	case KeyMCPSandbox:
		c.MCP.SandboxEnabled = isTrue(value)
	case KeyMCPWorkDir:
		c.MCP.WorkDir = value
	case KeyMCPUsername:
		c.MCP.Username = value
	case KeyMCPPassword:
		c.MCP.Password = value
	case KeyMCPBaseURL:
		c.MCP.BaseURL = value
	case KeyMCPTransport:
		c.MCP.Transport = value
	case KeyMCPAPIKey:
		c.MCP.APIKey = value
	case KeyMCPExtra:
		c.MCP.Extra = unmarshalExtra(value)
	case KeyAgentMaxSteps:
		c.Agent.MaxSteps = atoi(value)
	case KeyAgentUseNative:
		c.Agent.UseNativeTools = isTrue(value)
	case KeyAgentMaxResultRows:
		c.Agent.MaxResultRows = atoi(value)
	case KeyAgentMemMaxHistory:
		c.Agent.MemoryMaxHistory = atoi(value)
	case KeyAgentMemSummaryThreshold:
		c.Agent.MemorySummaryThreshold = atoi(value)
	case KeyAgentMemRecentKeep:
		c.Agent.MemoryRecentKeep = atoi(value)
	case KeyLogSaveToFile:
		c.Log.SaveToFile = isTrue(value)
	case KeyLogDir:
		c.Log.Dir = value
	case KeyPromptBuiltin:
		c.Prompts.Builtin = value
	case KeyPromptRemote:
		c.Prompts.Remote = value
	case KeyUIShowDuration:
		c.UI.ShowDuration = isTrue(value)
	case KeyUIShowSteps:
		c.UI.ShowSteps = isTrue(value)
	case KeyUIShowImages:
		c.UI.ShowImages = isTrue(value)
	case KeyUITheme:
		c.UI.Theme = value
	case KeyUIAppTitle:
		c.UI.AppTitle = value
	case KeyUIAppSubtitle:
		c.UI.AppSubtitle = value
	case KeyUIWorkflow:
		c.UI.WorkflowSteps = value
	case KeyUIAdminPageSize:
		c.UI.AdminPageSize = atoi(value)
	case KeyUIChatPageSize:
		c.UI.ChatPageSize = atoi(value)
	case KeyUIPhoneRequired:
		c.UI.PhoneRequired = isTrue(value)
	case KeyUIPhoneVerify:
		c.UI.PhoneVerifyRequired = isTrue(value)
	case KeyAdminUser:
		// admin.username 仅用于登录鉴权，不进入运行配置。
	case KeyAdminPass:
		// admin.password 仅用于登录鉴权，不进入运行配置。
	default:
		// 未知 key 在 Update 入口已被拦截，这里兜底忽略。
	}
	return nil
}

// ---- 类型转换辅助 ----

func b(v bool) string { return map[bool]string{true: "true", false: "false"}[v] }
func isTrue(s string) bool {
	return s == "true" || s == "1" || s == "yes" || s == "on"
}

// marshalExtra 序列化额外 MCP 列表为 JSON 字符串（空列表存 "[]"）。
func marshalExtra(list []config.RemoteMCP) string {
	if len(list) == 0 {
		return "[]"
	}
	b, err := json.Marshal(list)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalExtra 从 JSON 字符串解析额外 MCP 列表（解析失败返回空）。
func unmarshalExtra(s string) []config.RemoteMCP {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var list []config.RemoteMCP
	if err := json.Unmarshal([]byte(s), &list); err != nil {
		return nil
	}
	return list
}

func f64(v float64) string  { return strconv.FormatFloat(v, 'f', -1, 64) }
func itoa(v int) string     { return strconv.Itoa(v) }
func atof(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
func atoi(s string) int     { v, _ := strconv.Atoi(s); return v }

// MarshalConfig 将配置序列化为 JSON（用于调试/导出）。
func MarshalConfig(c *config.Config) (string, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	return string(b), err
}
