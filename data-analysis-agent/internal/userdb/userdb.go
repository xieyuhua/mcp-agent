// Package userdb 提供 Web UI 的用户体系（注册/登录/会话令牌）与多轮对话持久化
// （会话 conversation + 消息 message），存储于 SQLite。
//
// 说明：这是 Agent 前端自身的账号体系，与 mcp-data-server 的数据权限账号相互独立。
package userdb

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// User 前端用户。
type User struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	Username  string    `gorm:"uniqueIndex;size:64" json:"username"`
	Phone     string    `gorm:"size:20;index" json:"phone,omitempty"`
	Prompt    string    `gorm:"type:text" json:"prompt,omitempty"`
	Role      string    `gorm:"size:32;index" json:"role"`
	Disabled  bool      `gorm:"not null;default:false" json:"disabled"`
	Salt      string    `gorm:"size:32" json:"-"`
	PassHash  string    `gorm:"size:128" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Role 前端用户角色（后台可动态增删改）。
type Role struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:32" json:"name"`
	DisplayName string    `gorm:"size:128" json:"display_name"`
	Description string    `gorm:"type:text" json:"description"`
	Permissions string    `gorm:"type:text" json:"permissions,omitempty"`
	IsBuiltin   bool      `gorm:"not null;default:false" json:"is_builtin"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AdminRole 后台管理员角色（与前端用户角色分离）。
type AdminRole struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:32" json:"name"`
	DisplayName string    `gorm:"size:128" json:"display_name"`
	Description string    `gorm:"type:text" json:"description"`
	Permissions string    `gorm:"type:text" json:"permissions,omitempty"`
	IsBuiltin   bool      `gorm:"not null;default:false" json:"is_builtin"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 显式指定表名。
func (AdminRole) TableName() string { return "admin_roles" }

// Session 登录会话令牌。
type Session struct {
	Token     string    `gorm:"primaryKey;size:64" json:"token"`
	UserID    string    `gorm:"index;size:64" json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Admin 后台管理员账号。
type Admin struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	Username  string    `gorm:"uniqueIndex;size:64" json:"username"`
	Role      string    `gorm:"size:32;index" json:"role"`
	Disabled  bool      `gorm:"not null;default:false" json:"disabled"`
	Salt      string    `gorm:"size:32" json:"-"`
	PassHash  string    `gorm:"size:128" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LLMCallLog LLM 调用日志。
type LLMCallLog struct {
	ID             uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	ConversationID string    `gorm:"index;size:64" json:"conversation_id"`
	UserID         string    `gorm:"index;size:64" json:"user_id"`
	Model          string    `gorm:"size:128" json:"model"`
	Provider       string    `gorm:"size:32" json:"provider"`
	Messages       string    `gorm:"type:text" json:"messages,omitempty"`
	Tools          string    `gorm:"type:text" json:"tools,omitempty"`
	Response       string    `gorm:"type:text" json:"response,omitempty"`
	DurationMs     int64     `json:"duration_ms"`
	Error          string    `gorm:"type:text" json:"error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// MCPCallLog MCP 工具调用日志。
type MCPCallLog struct {
	ID             uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	ConversationID string    `gorm:"index;size:64" json:"conversation_id"`
	UserID         string    `gorm:"index;size:64" json:"user_id"`
	ToolName       string    `gorm:"size:128;index" json:"tool_name"`
	ServerName     string    `gorm:"size:128" json:"server_name"`
	Args           string    `gorm:"type:text" json:"args,omitempty"`
	Result         string    `gorm:"type:text" json:"result,omitempty"`
	DurationMs     int64     `json:"duration_ms"`
	IsError        bool      `json:"is_error"`
	Error          string    `gorm:"type:text" json:"error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// TableName 显式指定表名。
func (Admin) TableName() string      { return "admins" }
func (LLMCallLog) TableName() string  { return "llm_call_logs" }
func (MCPCallLog) TableName() string  { return "mcp_call_logs" }
type Conversation struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	UserID    string    `gorm:"index;size:64" json:"user_id"`
	Title     string    `gorm:"size:255" json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Message 会话中的一条消息。role: user | assistant。
type Message struct {
	ID             uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	ConversationID string `gorm:"index;size:64" json:"conversation_id"`
	Role           string `gorm:"size:16" json:"role"`
	Content        string `gorm:"type:text" json:"content"`
	// Extra 存 assistant 的富结果（chart/rows/sql/steps 的 JSON），前端可回放。
	Extra     string    `gorm:"type:text" json:"extra,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store 用户与会话存储。
type Store struct {
	db *gorm.DB
}

// New 打开数据库并建表。
func New(dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("userdb path is empty")
	}
	db, err := gorm.Open(sqlite.Open(dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open user db %s: %w", dbPath, err)
	}
	if err := configurePool(db); err != nil {
		return nil, fmt.Errorf("configure user db pool: %w", err)
	}
	if err := db.AutoMigrate(&User{}, &Session{}, &Role{}, &AdminRole{}, &Conversation{}, &Message{}, &Admin{}, &LLMCallLog{}, &MCPCallLog{}); err != nil {
		return nil, fmt.Errorf("migrate user db: %w", err)
	}
	store := &Store{db: db}
	store.seedAdminRoles()
	return store, nil
}

// seedAdminRoles 初始化内置管理员角色，确保迁移后始终可用。
func (s *Store) seedAdminRoles() {
	_ = s.UpsertAdminRole(&AdminRole{
		Name:        "super_admin",
		DisplayName: "超级管理员",
		Description: "拥有所有后台权限",
		Permissions: "admin:all",
		IsBuiltin:   true,
	})
	_ = s.UpsertAdminRole(&AdminRole{
		Name:        "admin",
		DisplayName: "管理员",
		Description: "常规管理员，可查看和修改大部分配置",
		Permissions: "config:read,config:write,user:read,user:write,role:read,role:write,admin:read,admin_role:read,chat_log:read,llm_log:read,mcp_log:read",
		IsBuiltin:   true,
	})
	_ = s.UpsertAdminRole(&AdminRole{
		Name:        "viewer",
		DisplayName: "只读管理员",
		Description: "仅可查看配置、用户和日志",
		Permissions: "config:read,user:read,role:read,admin:read,admin_role:read,chat_log:read,llm_log:read,mcp_log:read",
		IsBuiltin:   true,
	})
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
		log.Printf("warn: userdb WAL mode: %v", err)
	}
	if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil {
		log.Printf("warn: userdb busy_timeout: %v", err)
	}
	return nil
}

var (
	// ErrUserExists 用户名已存在。
	ErrUserExists = errors.New("用户名已存在")
	// ErrInvalidCredentials 用户名或密码错误。
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	// ErrUnauthorized 未登录或令牌失效。
	ErrUnauthorized = errors.New("未登录或登录已过期")
	// ErrNotFound 资源不存在或无权访问。
	ErrNotFound = errors.New("资源不存在或无权访问")
)

// ---- 用户 ----

// Register 注册新用户。
func (s *Store) Register(username, phone, password string, phoneRequired bool) (*User, error) {
	username = strings.TrimSpace(username)
	phone = strings.TrimSpace(phone)
	if len(username) < 2 || len(password) < 4 {
		return nil, errors.New("用户名至少2位、密码至少4位")
	}
	if phoneRequired && !isValidPhone(phone) {
		return nil, errors.New("请输入有效的手机号")
	}
	var cnt int64
	if err := s.db.Model(&User{}).Where("username = ?", username).Count(&cnt).Error; err != nil {
		return nil, err
	}
	if cnt > 0 {
		return nil, ErrUserExists
	}
	salt := randHex(16)
	u := &User{
		ID:        uuid.NewString(),
		Username:  username,
		Phone:     phone,
		Salt:      salt,
		PassHash:  hashPassword(password, salt),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.db.Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// isValidPhone 简单校验中国大陆手机号格式。
func isValidPhone(phone string) bool {
	if phone == "" {
		return false
	}
	// 必须以 1 开头，第二位 3-9，共 11 位数字
	if len(phone) != 11 {
		return false
	}
	if phone[0] != '1' {
		return false
	}
	second := phone[1]
	if second < '3' || second > '9' {
		return false
	}
	for i := 0; i < len(phone); i++ {
		if phone[i] < '0' || phone[i] > '9' {
			return false
		}
	}
	return true
}

// Login 校验用户名密码，成功返回一个新的会话令牌。
func (s *Store) Login(username, password string) (*User, string, error) {
	var u User
	if err := s.db.Where("username = ?", strings.TrimSpace(username)).First(&u).Error; err != nil {
		return nil, "", ErrInvalidCredentials
	}
	if u.Disabled {
		return nil, "", errors.New("账号已被禁用，请联系管理员")
	}
	if hashPassword(password, u.Salt) != u.PassHash {
		return nil, "", ErrInvalidCredentials
	}
	token := randHex(24)
	sess := &Session{
		Token:     token,
		UserID:    u.ID,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		CreatedAt: time.Now(),
	}
	if err := s.db.Create(sess).Error; err != nil {
		return nil, "", err
	}
	return &u, token, nil
}

// Logout 使令牌失效。
func (s *Store) Logout(token string) error {
	return s.db.Where("token = ?", token).Delete(&Session{}).Error
}

// UserByToken 通过会话令牌解析用户；令牌失效或过期返回 ErrUnauthorized。
func (s *Store) UserByToken(token string) (*User, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	var sess Session
	if err := s.db.Where("token = ?", token).First(&sess).Error; err != nil {
		return nil, ErrUnauthorized
	}
	if time.Now().After(sess.ExpiresAt) {
		// 过期会话清理失败不影响本次鉴权结果，仅记录日志。
		if err := s.db.Where("token = ?", token).Delete(&Session{}).Error; err != nil {
			log.Printf("warn: cleanup expired session %s: %v", token, err)
		}
		return nil, ErrUnauthorized
	}
	var u User
	if err := s.db.Where("id = ?", sess.UserID).First(&u).Error; err != nil {
		return nil, ErrUnauthorized
	}
	return &u, nil
}

// UserPrompt 返回用户的自定义提示词。
func (s *Store) UserPrompt(userID string) (string, error) {
	var u User
	if err := s.db.Where("id = ?", userID).First(&u).Error; err != nil {
		return "", err
	}
	return u.Prompt, nil
}

// SetUserPrompt 更新用户的自定义提示词。
func (s *Store) SetUserPrompt(userID, prompt string) error {
	return s.db.Model(&User{}).Where("id = ?", userID).Update("prompt", prompt).Error
}

// ---- 用户管理（后台） ----

// UserView 后台展示的用户视图。
type UserView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	DisplayRole string `json:"display_role,omitempty"`
	Disabled    bool   `json:"disabled"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ListUsersRequest 用户列表查询参数。
type ListUsersRequest struct {
	Search string
	Role   string
	Page   int
	Size   int
}

// ListUsers 分页列出用户（支持用户名搜索和角色筛选）。
func (s *Store) ListUsers(req ListUsersRequest) ([]UserView, int64, error) {
	if req.Page < 1 {
		req.Page = 1
	}
	if req.Size <= 0 || req.Size > 200 {
		req.Size = 20
	}
	query := s.db.Model(&User{})
	if req.Search != "" {
		query = query.Where("username LIKE ?", "%"+req.Search+"%")
	}
	if req.Role != "" {
		query = query.Where("role = ?", req.Role)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []User
	offset := (req.Page - 1) * req.Size
	if err := query.Order("created_at desc").Limit(req.Size).Offset(offset).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	roles, _ := s.ListRoles()
	roleMap := map[string]string{}
	for _, r := range roles {
		roleMap[r.Name] = r.DisplayName
	}
	views := make([]UserView, 0, len(users))
	for _, u := range users {
		views = append(views, UserView{
			ID:          u.ID,
			Username:    u.Username,
			Role:        u.Role,
			DisplayRole: roleMap[u.Role],
			Disabled:    u.Disabled,
			CreatedAt:   u.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   u.UpdatedAt.Format(time.RFC3339),
		})
	}
	return views, total, nil
}

// AdminCreateUser 后台管理员创建用户（可指定密码和角色）。
func (s *Store) AdminCreateUser(username, phone, password, role string) (*User, error) {
	username = strings.TrimSpace(username)
	phone = strings.TrimSpace(phone)
	if len(username) < 2 || len(password) < 4 {
		return nil, errors.New("用户名至少2位、密码至少4位")
	}
	if role == "" {
		role = "user"
	}
	var cnt int64
	if err := s.db.Model(&User{}).Where("username = ?", username).Count(&cnt).Error; err != nil {
		return nil, err
	}
	if cnt > 0 {
		return nil, ErrUserExists
	}
	// 角色不存在则自动创建为普通角色
	if role != "" {
		var rc int64
		_ = s.db.Model(&Role{}).Where("name = ?", role).Count(&rc)
		if rc == 0 {
			_ = s.UpsertRole(&Role{Name: role, DisplayName: role})
		}
	}
	salt := randHex(16)
	u := &User{
		ID:        uuid.NewString(),
		Username:  username,
		Phone:     phone,
		Role:      role,
		Disabled:  false,
		Salt:      salt,
		PassHash:  hashPassword(password, salt),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.db.Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// DeleteUser 删除前端用户及其会话、消息和会话记录。
func (s *Store) DeleteUser(userID string) error {
	var convs []Conversation
	if err := s.db.Where("user_id = ?", userID).Find(&convs).Error; err != nil {
		return err
	}
	for _, c := range convs {
		if err := s.db.Where("conversation_id = ?", c.ID).Delete(&Message{}).Error; err != nil {
			return err
		}
	}
	if err := s.db.Where("user_id = ?", userID).Delete(&Conversation{}).Error; err != nil {
		return err
	}
	if err := s.db.Where("user_id = ?", userID).Delete(&Session{}).Error; err != nil {
		return err
	}
	return s.db.Where("id = ?", userID).Delete(&User{}).Error
}

// SetUserDisabled 启用/禁用用户。
func (s *Store) SetUserDisabled(userID string, disabled bool) error {
	return s.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"disabled":   disabled,
		"updated_at": time.Now(),
	}).Error
}

// SetUserPassword 重置用户密码。
func (s *Store) SetUserPassword(userID, password string) error {
	if len(password) < 4 {
		return errors.New("密码至少4位")
	}
	salt := randHex(16)
	return s.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"salt":       salt,
		"pass_hash":  hashPassword(password, salt),
		"updated_at": time.Now(),
	}).Error
}

// SetUserRole 修改用户角色。
func (s *Store) SetUserRole(userID, role string) error {
	if role == "" {
		role = "user"
	}
	return s.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"role":       role,
		"updated_at": time.Now(),
	}).Error
}

// ImportUsersCSV 从 CSV 导入用户。列：username,password,role,disabled（可选）。
func (s *Store) ImportUsersCSV(r *bytes.Buffer) (int, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}
	idx := csvHeaderIndex(records[0])
	if _, ok := idx["username"]; !ok {
		return 0, errors.New("CSV 必须包含 username 列")
	}
	imported := 0
	for i, row := range records[1:] {
		username := csvVal(row, idx, "username")
		password := csvVal(row, idx, "password")
		role := csvVal(row, idx, "role")
		if role == "" {
			role = "user"
		}
		disabled := false
		if v := csvVal(row, idx, "disabled"); v != "" {
			disabled, _ = strconv.ParseBool(v)
		}
		if username == "" || password == "" {
			return imported, fmt.Errorf("第 %d 行：用户名或密码为空", i+2)
		}
		var u User
		err := s.db.Where("username = ?", username).First(&u).Error
		now := time.Now()
		if err == nil {
			// 更新已有用户：仅改密码、角色、禁用状态
			if err := s.SetUserPassword(u.ID, password); err != nil {
				return imported, fmt.Errorf("第 %d 行：%w", i+2, err)
			}
			_ = s.SetUserRole(u.ID, role)
			_ = s.SetUserDisabled(u.ID, disabled)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			salt := randHex(16)
			u = User{
				ID:        uuid.NewString(),
				Username:  username,
				Role:      role,
				Disabled:  disabled,
				Salt:      salt,
				PassHash:  hashPassword(password, salt),
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := s.db.Create(&u).Error; err != nil {
				return imported, fmt.Errorf("第 %d 行：%w", i+2, err)
			}
		} else {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// ExportUsersCSV 导出用户列表为 CSV（不含密码）。
func (s *Store) ExportUsersCSV() ([]byte, error) {
	var users []User
	if err := s.db.Order("created_at desc").Find(&users).Error; err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	// UTF-8 BOM for Excel
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(buf)
	_ = w.Write([]string{"username", "role", "disabled", "created_at"})
	for _, u := range users {
		_ = w.Write([]string{u.Username, u.Role, strconv.FormatBool(u.Disabled), u.CreatedAt.Format(time.RFC3339)})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// ---- 角色管理（后台） ----

// ListRoles 列出所有角色。
func (s *Store) ListRoles() ([]Role, error) {
	var list []Role
	err := s.db.Order("created_at asc").Find(&list).Error
	return list, err
}

// UpsertRole 新增或更新角色（仅 name 唯一；内置角色不可改 name）。
func (s *Store) UpsertRole(role *Role) error {
	role.Name = strings.TrimSpace(role.Name)
	if role.Name == "" {
		return errors.New("角色标识不能为空")
	}
	role.UpdatedAt = time.Now()
	if role.CreatedAt.IsZero() {
		role.CreatedAt = role.UpdatedAt
	}
	var existing Role
	err := s.db.Where("name = ?", role.Name).First(&existing).Error
	if err == nil {
		if existing.IsBuiltin {
			role.IsBuiltin = true
		}
		role.ID = existing.ID
		role.CreatedAt = existing.CreatedAt
		// 保留已有权限，除非本次显式传入
		if role.Permissions == "" && existing.Permissions != "" {
			role.Permissions = existing.Permissions
		}
		return s.db.Save(role).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.Create(role).Error
	}
	return err
}

// DeleteRole 删除非内置角色。
func (s *Store) DeleteRole(name string) error {
	var r Role
	if err := s.db.Where("name = ?", name).First(&r).Error; err != nil {
		return err
	}
	if r.IsBuiltin {
		return errors.New("内置角色不能删除")
	}
	// 把使用此角色的用户重置为默认 user
	if err := s.db.Model(&User{}).Where("role = ?", name).Update("role", "user").Error; err != nil {
		return err
	}
	return s.db.Where("name = ?", name).Delete(&Role{}).Error
}

// ---- 沟通日志（后台） ----

// ChatLogView 沟通日志视图。
type ChatLogView struct {
	ID             uint   `json:"id"`
	ConversationID string `json:"conversation_id"`
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	Role           string `json:"role"`
	Content        string `json:"content"`
	Extra          string `json:"extra,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// ChatLogFilter 沟通日志筛选条件。
type ChatLogFilter struct {
	Username string
	Role     string // user | assistant
	Keyword  string
	DateFrom string // ISO8601
	DateTo   string // ISO8601
	Page     int
	Size     int
}

// ListChatLogs 分页查询沟通日志（按用户名、角色、关键词、时间范围筛选）。
func (s *Store) ListChatLogs(f ChatLogFilter) ([]ChatLogView, int64, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Size <= 0 || f.Size > 200 {
		f.Size = 20
	}
	query := s.db.Model(&Message{}).Select(
		"messages.id, messages.conversation_id, conversations.user_id, users.username, messages.role, messages.content, messages.extra, messages.created_at").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Joins("JOIN users ON users.id = conversations.user_id")
	if f.Username != "" {
		query = query.Where("users.username LIKE ?", "%"+f.Username+"%")
	}
	if f.Role != "" {
		query = query.Where("messages.role = ?", f.Role)
	}
	if f.Keyword != "" {
		query = query.Where("messages.content LIKE ?", "%"+f.Keyword+"%")
	}
	if f.DateFrom != "" {
		query = query.Where("messages.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != "" {
		query = query.Where("messages.created_at <= ?", f.DateTo+" 23:59:59")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []ChatLogView
	offset := (f.Page - 1) * f.Size
	err := query.Order("messages.created_at desc").Limit(f.Size).Offset(offset).Scan(&rows).Error
	return rows, total, err
}

// ---- 调用日志（后台） ----

// InsertLLMCallLog 写入 LLM 调用日志。
func (s *Store) InsertLLMCallLog(userID, convID, model, provider, messages, tools, response string, durationMs int64, errorMsg string) error {
	log := &LLMCallLog{
		ConversationID: convID,
		UserID:         userID,
		Model:          model,
		Provider:       provider,
		Messages:       messages,
		Tools:          tools,
		Response:       response,
		DurationMs:     durationMs,
		Error:          errorMsg,
		CreatedAt:      time.Now(),
	}
	return s.db.Create(log).Error
}

// InsertMCPCallLog 写入 MCP 工具调用日志。
func (s *Store) InsertMCPCallLog(userID, convID, toolName, serverName, args, result string, durationMs int64, isErr bool, errorMsg string) error {
	log := &MCPCallLog{
		ConversationID: convID,
		UserID:         userID,
		ToolName:       toolName,
		ServerName:     serverName,
		Args:           args,
		Result:         result,
		DurationMs:     durationMs,
		IsError:        isErr,
		Error:          errorMsg,
		CreatedAt:      time.Now(),
	}
	return s.db.Create(log).Error
}

// LLMLogFilter LLM 调用日志筛选条件。
type LLMLogFilter struct {
	Username string
	Model    string
	Keyword  string
	DateFrom string
	DateTo   string
	Page     int
	Size     int
}

// LLMLogView LLM 调用日志视图。
type LLMLogView struct {
	ID             uint   `json:"id"`
	ConversationID string `json:"conversation_id"`
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	Response       string `json:"response,omitempty"`
	DurationMs     int64  `json:"duration_ms"`
	Error          string `json:"error,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// ListLLMCallLogs 分页查询 LLM 调用日志。
func (s *Store) ListLLMCallLogs(f LLMLogFilter) ([]LLMLogView, int64, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Size <= 0 || f.Size > 200 {
		f.Size = 20
	}
	query := s.db.Model(&LLMCallLog{}).Select(
		"llm_call_logs.id, llm_call_logs.conversation_id, llm_call_logs.user_id, users.username, llm_call_logs.model, llm_call_logs.provider, llm_call_logs.response, llm_call_logs.duration_ms, llm_call_logs.error, llm_call_logs.created_at").
		Joins("JOIN users ON users.id = llm_call_logs.user_id")
	if f.Username != "" {
		query = query.Where("users.username LIKE ?", "%"+f.Username+"%")
	}
	if f.Model != "" {
		query = query.Where("llm_call_logs.model = ?", f.Model)
	}
	if f.Keyword != "" {
		query = query.Where("llm_call_logs.response LIKE ? OR llm_call_logs.error LIKE ?", "%"+f.Keyword+"%", "%"+f.Keyword+"%")
	}
	if f.DateFrom != "" {
		query = query.Where("llm_call_logs.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != "" {
		query = query.Where("llm_call_logs.created_at <= ?", f.DateTo+" 23:59:59")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []LLMLogView
	offset := (f.Page - 1) * f.Size
	err := query.Order("llm_call_logs.created_at desc").Limit(f.Size).Offset(offset).Scan(&rows).Error
	return rows, total, err
}

// MCPLogFilter MCP 调用日志筛选条件。
type MCPLogFilter struct {
	Username string
	ToolName string
	Keyword  string
	DateFrom string
	DateTo   string
	Page     int
	Size     int
}

// MCPLogView MCP 调用日志视图。
type MCPLogView struct {
	ID             uint   `json:"id"`
	ConversationID string `json:"conversation_id"`
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	ToolName       string `json:"tool_name"`
	ServerName     string `json:"server_name"`
	Result         string `json:"result,omitempty"`
	DurationMs     int64  `json:"duration_ms"`
	IsError        bool   `json:"is_error"`
	Error          string `json:"error,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// ListMCPCallLogs 分页查询 MCP 调用日志。
func (s *Store) ListMCPCallLogs(f MCPLogFilter) ([]MCPLogView, int64, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Size <= 0 || f.Size > 200 {
		f.Size = 20
	}
	query := s.db.Model(&MCPCallLog{}).Select(
		"mcp_call_logs.id, mcp_call_logs.conversation_id, mcp_call_logs.user_id, users.username, mcp_call_logs.tool_name, mcp_call_logs.server_name, mcp_call_logs.result, mcp_call_logs.duration_ms, mcp_call_logs.is_error, mcp_call_logs.error, mcp_call_logs.created_at").
		Joins("JOIN users ON users.id = mcp_call_logs.user_id")
	if f.Username != "" {
		query = query.Where("users.username LIKE ?", "%"+f.Username+"%")
	}
	if f.ToolName != "" {
		query = query.Where("mcp_call_logs.tool_name = ?", f.ToolName)
	}
	if f.Keyword != "" {
		query = query.Where("mcp_call_logs.result LIKE ? OR mcp_call_logs.error LIKE ?", "%"+f.Keyword+"%", "%"+f.Keyword+"%")
	}
	if f.DateFrom != "" {
		query = query.Where("mcp_call_logs.created_at >= ?", f.DateFrom)
	}
	if f.DateTo != "" {
		query = query.Where("mcp_call_logs.created_at <= ?", f.DateTo+" 23:59:59")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []MCPLogView
	offset := (f.Page - 1) * f.Size
	err := query.Order("mcp_call_logs.created_at desc").Limit(f.Size).Offset(offset).Scan(&rows).Error
	return rows, total, err
}

// ---- 管理员账号（后台） ----

// AdminView 后台展示的管理员视图。
type AdminView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	DisplayRole string `json:"display_role,omitempty"`
	Disabled    bool   `json:"disabled"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// AdminLogin 校验管理员账号密码。
func (s *Store) AdminLogin(username, password string) (*Admin, error) {
	var a Admin
	if err := s.db.Where("username = ?", strings.TrimSpace(username)).First(&a).Error; err != nil {
		return nil, ErrInvalidCredentials
	}
	if a.Disabled {
		return nil, errors.New("账号已被禁用")
	}
	if hashPassword(password, a.Salt) != a.PassHash {
		return nil, ErrInvalidCredentials
	}
	return &a, nil
}

// ListAdmins 列出所有管理员。
func (s *Store) ListAdmins() ([]AdminView, error) {
	var list []Admin
	if err := s.db.Order("created_at asc").Find(&list).Error; err != nil {
		return nil, err
	}
	roles, _ := s.ListAdminRoles()
	roleMap := map[string]string{}
	for _, r := range roles {
		roleMap[r.Name] = r.DisplayName
	}
	views := make([]AdminView, 0, len(list))
	for _, a := range list {
		views = append(views, AdminView{
			ID:          a.ID,
			Username:    a.Username,
			Role:        a.Role,
			DisplayRole: roleMap[a.Role],
			Disabled:    a.Disabled,
			CreatedAt:   a.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   a.UpdatedAt.Format(time.RFC3339),
		})
	}
	return views, nil
}

// CreateAdmin 创建管理员账号。
func (s *Store) CreateAdmin(username, password, role string) (*Admin, error) {
	username = strings.TrimSpace(username)
	if len(username) < 2 || len(password) < 4 {
		return nil, errors.New("账号至少2位、密码至少4位")
	}
	if role == "" {
		role = "admin"
	}
	var cnt int64
	if err := s.db.Model(&Admin{}).Where("username = ?", username).Count(&cnt).Error; err != nil {
		return nil, err
	}
	if cnt > 0 {
		return nil, ErrUserExists
	}
	salt := randHex(16)
	a := &Admin{
		ID:        uuid.NewString(),
		Username:  username,
		Role:      role,
		Disabled:  false,
		Salt:      salt,
		PassHash:  hashPassword(password, salt),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.db.Create(a).Error; err != nil {
		return nil, err
	}
	return a, nil
}

// SetAdminPassword 重置管理员密码。
func (s *Store) SetAdminPassword(id, password string) error {
	if len(password) < 4 {
		return errors.New("密码至少4位")
	}
	salt := randHex(16)
	return s.db.Model(&Admin{}).Where("id = ?", id).Updates(map[string]interface{}{
		"salt":       salt,
		"pass_hash":  hashPassword(password, salt),
		"updated_at": time.Now(),
	}).Error
}

// SetAdminDisabled 启用/禁用管理员。
func (s *Store) SetAdminDisabled(id string, disabled bool) error {
	return s.db.Model(&Admin{}).Where("id = ?", id).Updates(map[string]interface{}{
		"disabled":   disabled,
		"updated_at": time.Now(),
	}).Error
}

// SetAdminRole 修改管理员角色。
func (s *Store) SetAdminRole(id, role string) error {
	if role == "" {
		role = "admin"
	}
	return s.db.Model(&Admin{}).Where("id = ?", id).Updates(map[string]interface{}{
		"role":       role,
		"updated_at": time.Now(),
	}).Error
}

// DeleteAdmin 删除管理员（不能删除最后一个管理员）。
func (s *Store) DeleteAdmin(id string) error {
	var cnt int64
	if err := s.db.Model(&Admin{}).Count(&cnt).Error; err != nil {
		return err
	}
	if cnt <= 1 {
		return errors.New("至少保留一个管理员账号")
	}
	return s.db.Where("id = ?", id).Delete(&Admin{}).Error
}

// ---- 管理员角色（后台） ----

// ListAdminRoles 列出所有管理员角色。
func (s *Store) ListAdminRoles() ([]AdminRole, error) {
	var list []AdminRole
	err := s.db.Order("created_at asc").Find(&list).Error
	return list, err
}

// UpsertAdminRole 新增或更新管理员角色。
func (s *Store) UpsertAdminRole(role *AdminRole) error {
	role.Name = strings.TrimSpace(role.Name)
	if role.Name == "" {
		return errors.New("角色标识不能为空")
	}
	role.UpdatedAt = time.Now()
	if role.CreatedAt.IsZero() {
		role.CreatedAt = role.UpdatedAt
	}
	var existing AdminRole
	err := s.db.Where("name = ?", role.Name).First(&existing).Error
	if err == nil {
		if existing.IsBuiltin {
			role.IsBuiltin = true
		}
		role.ID = existing.ID
		role.CreatedAt = existing.CreatedAt
		if role.Permissions == "" && existing.Permissions != "" {
			role.Permissions = existing.Permissions
		}
		return s.db.Save(role).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.Create(role).Error
	}
	return err
}

// DeleteAdminRole 删除非内置管理员角色。
func (s *Store) DeleteAdminRole(name string) error {
	var r AdminRole
	if err := s.db.Where("name = ?", name).First(&r).Error; err != nil {
		return err
	}
	if r.IsBuiltin {
		return errors.New("内置角色不能删除")
	}
	if err := s.db.Model(&Admin{}).Where("role = ?", name).Update("role", "admin").Error; err != nil {
		return err
	}
	return s.db.Where("name = ?", name).Delete(&AdminRole{}).Error
}

// AdminRolePermissions 返回指定管理员角色的权限列表。
func (s *Store) AdminRolePermissions(roleName string) []string {
	if roleName == "" {
		return nil
	}
	var r AdminRole
	if err := s.db.Where("name = ?", roleName).First(&r).Error; err != nil {
		return nil
	}
	parts := strings.Split(r.Permissions, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// csvHeaderIndex 把 CSV 表头映射为列名->索引。
func csvHeaderIndex(headers []string) map[string]int {
	idx := make(map[string]int, len(headers))
	for i, h := range headers {
		idx[strings.TrimSpace(strings.ToLower(h))] = i
	}
	return idx
}

// csvVal 从 CSV 行中按列名读取并去空格。
func csvVal(row []string, idx map[string]int, key string) string {
	if i, ok := idx[key]; ok && i < len(row) {
		return strings.TrimSpace(row[i])
	}
	return ""
}

// ---- 会话 / 消息 ----

// CreateConversation 为用户创建新会话。
func (s *Store) CreateConversation(userID, title string) (*Conversation, error) {
	if title == "" {
		title = "新对话"
	}
	c := &Conversation{
		ID:        uuid.NewString(),
		UserID:    userID,
		Title:     title,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.db.Create(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// ListConversationsPaginated 分页列出用户的会话（按更新时间倒序）。
func (s *Store) ListConversationsPaginated(userID string, limit, offset int) ([]Conversation, int64, error) {
	var total int64
	if err := s.db.Model(&Conversation{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []Conversation
	err := s.db.Where("user_id = ?", userID).Order("updated_at desc").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// GetConversation 校验会话归属并返回。
func (s *Store) GetConversation(userID, convID string) (*Conversation, error) {
	var c Conversation
	if err := s.db.Where("id = ? AND user_id = ?", convID, userID).First(&c).Error; err != nil {
		return nil, ErrNotFound
	}
	return &c, nil
}

// DeleteConversation 删除会话及其消息。
func (s *Store) DeleteConversation(userID, convID string) error {
	if _, err := s.GetConversation(userID, convID); err != nil {
		return err
	}
	s.db.Where("conversation_id = ?", convID).Delete(&Message{})
	return s.db.Where("id = ? AND user_id = ?", convID, userID).Delete(&Conversation{}).Error
}

// RenameConversation 重命名会话标题。
func (s *Store) RenameConversation(userID, convID, title string) error {
	if _, err := s.GetConversation(userID, convID); err != nil {
		return err
	}
	return s.db.Model(&Conversation{}).Where("id = ?", convID).
		Updates(map[string]interface{}{"title": title, "updated_at": time.Now()}).Error
}

// ListMessages 返回会话内消息（按时间正序）。
func (s *Store) ListMessages(userID, convID string) ([]Message, error) {
	if _, err := s.GetConversation(userID, convID); err != nil {
		return nil, err
	}
	var list []Message
	err := s.db.Where("conversation_id = ?", convID).Order("id asc").Find(&list).Error
	return list, err
}

// ListMessagesPaginated 返回会话内消息分页（按时间正序）以及总条数。
func (s *Store) ListMessagesPaginated(userID, convID string, limit, offset int) ([]Message, int64, error) {
	if _, err := s.GetConversation(userID, convID); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := s.db.Model(&Message{}).Where("conversation_id = ?", convID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []Message
	query := s.db.Where("conversation_id = ?", convID).Order("id asc")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	err := query.Find(&list).Error
	return list, total, err
}

// AddMessage 追加一条消息，并刷新会话更新时间。
func (s *Store) AddMessage(convID, role, content, extra string) (*Message, error) {
	m := &Message{
		ConversationID: convID,
		Role:           role,
		Content:        content,
		Extra:          extra,
		CreatedAt:      time.Now(),
	}
	if err := s.db.Create(m).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&Conversation{}).Where("id = ?", convID).Update("updated_at", time.Now()).Error; err != nil {
		return nil, err
	}
	return m, nil
}

// ---- 辅助 ----

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashPassword(password, salt string) string {
	h := sha256.Sum256([]byte(salt + ":" + password))
	return hex.EncodeToString(h[:])
}
