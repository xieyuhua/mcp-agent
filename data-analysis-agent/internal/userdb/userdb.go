// Package userdb 提供 Web UI 的用户体系（注册/登录/会话令牌）与多轮对话持久化
// （会话 conversation + 消息 message），存储于 SQLite。
//
// 说明：这是 Agent 前端自身的账号体系，与 mcp-data-server 的数据权限账号相互独立。
package userdb

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// User 前端用户。
type User struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	Username  string    `gorm:"uniqueIndex;size:64" json:"username"`
	Salt      string    `gorm:"size:32" json:"-"`
	PassHash  string    `gorm:"size:128" json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// Session 登录会话令牌。
type Session struct {
	Token     string    `gorm:"primaryKey;size:64" json:"token"`
	UserID    string    `gorm:"index;size:64" json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Conversation 一次多轮对话会话。
type Conversation struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	UserID    string    `gorm:"index;size:64" json:"user_id"`
	Title     string    `gorm:"size:255" json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Message 会话中的一条消息。role: user | assistant。
type Message struct {
	ID             uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	ConversationID string    `gorm:"index;size:64" json:"conversation_id"`
	Role           string    `gorm:"size:16" json:"role"`
	Content        string    `gorm:"type:text" json:"content"`
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
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open user db %s: %w", dbPath, err)
	}
	if err := db.AutoMigrate(&User{}, &Session{}, &Conversation{}, &Message{}); err != nil {
		return nil, fmt.Errorf("migrate user db: %w", err)
	}
	return &Store{db: db}, nil
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
func (s *Store) Register(username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	if len(username) < 2 || len(password) < 4 {
		return nil, errors.New("用户名至少2位、密码至少4位")
	}
	var cnt int64
	s.db.Model(&User{}).Where("username = ?", username).Count(&cnt)
	if cnt > 0 {
		return nil, ErrUserExists
	}
	salt := randHex(16)
	u := &User{
		ID:        uuid.NewString(),
		Username:  username,
		Salt:      salt,
		PassHash:  hashPassword(password, salt),
		CreatedAt: time.Now(),
	}
	if err := s.db.Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// Login 校验用户名密码，成功返回一个新的会话令牌。
func (s *Store) Login(username, password string) (*User, string, error) {
	var u User
	if err := s.db.Where("username = ?", strings.TrimSpace(username)).First(&u).Error; err != nil {
		return nil, "", ErrInvalidCredentials
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
func (s *Store) Logout(token string) {
	s.db.Where("token = ?", token).Delete(&Session{})
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
		s.db.Where("token = ?", token).Delete(&Session{})
		return nil, ErrUnauthorized
	}
	var u User
	if err := s.db.Where("id = ?", sess.UserID).First(&u).Error; err != nil {
		return nil, ErrUnauthorized
	}
	return &u, nil
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

// ListConversations 列出用户的会话（按更新时间倒序）。
func (s *Store) ListConversations(userID string) ([]Conversation, error) {
	var list []Conversation
	err := s.db.Where("user_id = ?", userID).Order("updated_at desc").Find(&list).Error
	return list, err
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
	s.db.Model(&Conversation{}).Where("id = ?", convID).Update("updated_at", time.Now())
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
