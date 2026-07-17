package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/tenant"

	"gorm.io/gorm"
)

// tokenPayload 令牌载荷（HMAC 签名，无外部依赖）。
type tokenPayload struct {
	TenantID string `json:"tid"`
	UserID   string `json:"uid"`
	Role     string `json:"role"`
	RegionID string `json:"rid"`
	StoreID  string `json:"sid"`
	Exp      int64  `json:"exp"`
}

// AuthService 鉴权服务：登录、签发与校验令牌。
type AuthService struct {
	db     *gorm.DB
	secret string
}

func NewAuthService(db *gorm.DB, secret string) *AuthService {
	return &AuthService{db: db, secret: secret}
}

func (s *AuthService) sign(b []byte) string {
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write(b)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Login 校验用户名/密码，成功返回令牌。
func (s *AuthService) Login(username, password string) (string, *tenant.Context, error) {
	var u model.User
	if err := s.db.Where("username = ?", username).First(&u).Error; err != nil {
		return "", nil, fmt.Errorf("invalid username or password")
	}
	h := sha256.Sum256([]byte(password))
	if hexEncode(h[:]) != u.Password {
		return "", nil, fmt.Errorf("invalid username or password")
	}
	tok, err := s.IssueToken(&u)
	if err != nil {
		return "", nil, err
	}
	tc := &tenant.Context{
		TenantID: u.TenantID,
		UserID:   u.ID,
		Role:     u.Role,
		RegionID: u.RegionID,
		StoreID:  u.StoreID,
	}
	return tok, tc, nil
}

// IssueToken 为用户签发令牌。
func (s *AuthService) IssueToken(u *model.User) (string, error) {
	p := tokenPayload{
		TenantID: u.TenantID,
		UserID:   u.ID,
		Role:     u.Role,
		RegionID: u.RegionID,
		StoreID:  u.StoreID,
		Exp:      time.Now().Add(12 * time.Hour).Unix(),
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b) + "." + s.sign(b), nil
}

// VerifyToken 校验并解析令牌。
func (s *AuthService) VerifyToken(tok string) (*tenant.Context, error) {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed token")
	}
	raw, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("malformed token payload")
	}
	if s.sign(raw) != parts[1] {
		return nil, fmt.Errorf("token signature mismatch")
	}
	var p tokenPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("token decode error")
	}
	if p.Exp > 0 && time.Now().Unix() > p.Exp {
		return nil, fmt.Errorf("token expired")
	}
	return &tenant.Context{
		TenantID: p.TenantID,
		UserID:   p.UserID,
		Role:     p.Role,
		RegionID: p.RegionID,
		StoreID:  p.StoreID,
	}, nil
}

func hexEncode(b []byte) string {
	return fmt.Sprintf("%x", b)
}
