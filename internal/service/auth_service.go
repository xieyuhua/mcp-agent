package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/repository"

	"gorm.io/gorm"
)

// AuthService 负责后台账号的认证与会话管理。
// 设计取舍：使用标准库 sha256 + 随机 salt 做口令哈希，使用 HMAC 签名 cookie 做会话，
// 不引入 JWT / bcrypt 等外部依赖，保持后端轻量。
type AuthService struct {
	repo       *repository.PermissionRepo
	secret     []byte
	sessionTTL time.Duration
}

// NewAuthService 创建认证服务。secret 为会话签名密钥，应来自配置且保持稳定。
func NewAuthService(repo *repository.PermissionRepo, secret string) *AuthService {
	s := secret
	if s == "" {
		s = "mcp-data-server-default-secret-change-me"
	}
	return &AuthService{
		repo:       repo,
		secret:     []byte(s),
		sessionTTL: 24 * time.Hour,
	}
}

// HashPassword 生成随机 salt 并返回 (salt, hash)。
// 哈希算法：sha256(salt + password)。
func HashPassword(password string) (salt, hash string) {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	salt = base64.StdEncoding.EncodeToString(buf)
	return salt, deriveHash(salt, password)
}

func deriveHash(salt, password string) string {
	h := sha256.New()
	h.Write([]byte(salt))
	h.Write([]byte(password))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Authenticate 校验用户名与密码，成功返回用户（密码字段已置空）。
func (s *AuthService) Authenticate(username, password string) (*model.AdminUser, error) {
	u, err := s.repo.GetAdminUser(username)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("账号或密码错误")
		}
		return nil, err
	}
	if deriveHash(u.Salt, password) != u.PasswordHash {
		return nil, errors.New("账号或密码错误")
	}
	clean := *u
	clean.PasswordHash = ""
	clean.Salt = ""
	return &clean, nil
}

// ChangePassword 修改指定用户密码。会校验旧密码（首次必须改密时 oldPassword 可为空）。
func (s *AuthService) ChangePassword(username, oldPassword, newPassword string) error {
	if len(newPassword) < 6 {
		return errors.New("新密码长度至少 6 位")
	}
	u, err := s.repo.GetAdminUser(username)
	if err != nil {
		return err
	}
	// 非首次改密必须校验旧密码
	if !u.MustChange {
		if oldPassword == "" {
			return errors.New("请提供原密码")
		}
		if deriveHash(u.Salt, oldPassword) != u.PasswordHash {
			return errors.New("原密码错误")
		}
	}
	salt, hash := HashPassword(newPassword)
	u.Salt = salt
	u.PasswordHash = hash
	u.MustChange = false
	if err := s.repo.SaveAdminUser(u); err != nil {
		return err
	}
	// 改密后吊销所有已有会话（旧 token 立即失效），需在下次登录时重新签发
	return s.InvalidateSessions(username)
}

// IssueSession 为已认证用户签发会话 token（HMAC 签名，无状态）。
// token 内写入 session_version，使服务端可在改密/退出时主动吊销。
func (s *AuthService) IssueSession(username, displayName string) (string, error) {
	u, err := s.repo.GetAdminUser(username)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]interface{}{
		"u": username,
		"n": displayName,
		"v": u.SessionVersion,
		"exp": time.Now().Add(s.sessionTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString(payload)
	sig := sign([]byte(b64), s.secret)
	return b64 + "." + sig, nil
}

// VerifySession 校验会话 token，返回其中的用户名。
// 除签名与过期外，还会比对 admin_users.session_version：
// 若用户改密或主动退出导致版本号变化，旧 token 立即失效。
func (s *AuthService) VerifySession(token string) (username, displayName string, ok bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	if !validSig([]byte(parts[0]), parts[1], s.secret) {
		return "", "", false
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(decodeB64(parts[0])), &m); err != nil {
		return "", "", false
	}
	exp, _ := m["exp"].(float64)
	if time.Now().Unix() > int64(exp) {
		return "", "", false
	}
	u, _ := m["u"].(string)
	n, _ := m["n"].(string)
	if u == "" {
		return "", "", false
	}
	// 比对会话版本号，实现服务端吊销
	stored, err := s.repo.GetAdminUser(u)
	if err != nil {
		return "", "", false
	}
	v, _ := m["v"].(float64)
	if int64(v) != stored.SessionVersion {
		return "", "", false
	}
	return u, n, true
}

// InvalidateSessions 使该用户的所有已有会话立即失效（退出登录/改密时调用）。
// 通过递增 session_version 实现：旧 token 携带的旧版本号将不再匹配。
func (s *AuthService) InvalidateSessions(username string) error {
	u, err := s.repo.GetAdminUser(username)
	if err != nil {
		return err
	}
	u.SessionVersion++
	return s.repo.SaveAdminUser(u)
}

func sign(data, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func validSig(data []byte, sig string, secret []byte) bool {
	expected := sign(data, secret)
	return hmac.Equal([]byte(expected), []byte(sig))
}

func decodeB64(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// EnsureBootstrapUser 首次安装：当没有任何账号时，创建默认 admin 账号并返回其初始密码。
// 返回的 password 为空表示已有账号、无需初始化。
func (s *AuthService) EnsureBootstrapUser() (username, password string, created bool, err error) {
	count, err := s.repo.CountAdminUsers()
	if err != nil {
		return "", "", false, err
	}
	if count > 0 {
		return "", "", false, nil
	}
	pw := randomPassword(12)
	salt, hash := HashPassword(pw)
	u := &model.AdminUser{
		Username:     "admin",
		Salt:         salt,
		PasswordHash: hash,
		DisplayName:  "管理员",
		Role:         "admin",
		MustChange:   true,
	}
	if err := s.repo.SaveAdminUser(u); err != nil {
		return "", "", false, err
	}
	return "admin", pw, true, nil
}

func randomPassword(n int) string {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = chars[int(b)%len(chars)]
	}
	return string(out)
}

// formatBootstrapBanner 生成首次安装输出的账号密码提示横幅。
func FormatBootstrapBanner(username, password string) string {
	return fmt.Sprintf(
		"\n"+
			"=========================================================\n"+
			"  首次安装：已自动创建后台管理员账号\n"+
			"  账号：%s\n"+
			"  初始密码：%s\n"+
			"  请尽快登录后台修改密码（首次登录强制改密）。\n"+
			"  管理后台地址：/admin\n"+
			"=========================================================\n",
		username, password,
	)
}
