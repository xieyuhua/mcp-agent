package handler

import (
	"net/http"
	"time"

	"company.com/mcp-data-server/internal/service"

	"github.com/gin-gonic/gin"
)

const sessionCookieName = "mcp_admin_session"

// AuthHandler 处理后台登录、登出、改密与当前用户查询。
type AuthHandler struct {
	auth *service.AuthService
}

func NewAuthHandler(auth *service.AuthService) *AuthHandler {
	return &AuthHandler{auth: auth}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// login 校验账号密码并写入会话 cookie。
func (h *AuthHandler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"error": "请求格式错误"})
		return
	}
	user, err := h.auth.Authenticate(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	token, err := h.auth.IssueSession(user.Username, user.DisplayName)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": "生成会话失败: " + err.Error()})
		return
	}
	c.SetCookie(sessionCookieName, token, int((24 * time.Hour).Seconds()),
		"/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"data": user})
}

// logout 退出登录：吊销该用户所有会话（递增 session_version），并清除浏览器会话 cookie。
// 旧 token 立即失效，即使仍在 24h 有效期内也无法再通过校验。
func (h *AuthHandler) logout(c *gin.Context) {
	if cookie, err := c.Cookie(sessionCookieName); err == nil && cookie != "" {
		if username, _, ok := h.auth.VerifySession(cookie); ok {
			_ = h.auth.InvalidateSessions(username)
		}
	}
	c.SetCookie(sessionCookieName, "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"data": nil})
}

// currentUser 返回当前登录用户信息（由 requireLogin 保证已认证）。
func (h *AuthHandler) currentUser(c *gin.Context) {
	u, _ := c.Get("admin_user")
	c.JSON(http.StatusOK, gin.H{"data": u})
}

type changePwdRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// changePassword 修改当前登录用户的密码。
// 改密会吊销所有旧会话；这里在成功后为本机重新签发会话，使用户免登出即可继续使用。
func (h *AuthHandler) changePassword(c *gin.Context) {
	username, _ := c.Get("admin_username")
	var req changePwdRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"error": "请求格式错误"})
		return
	}
	if err := h.auth.ChangePassword(username.(string), req.OldPassword, req.NewPassword); err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}
	// 改密后旧 token 已失效，为本机重新签发新会话（version 已 +1）
	user, err := h.auth.Authenticate(username.(string), req.NewPassword)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": nil})
		return
	}
	token, err := h.auth.IssueSession(user.Username, user.DisplayName)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": nil})
		return
	}
	c.SetCookie(sessionCookieName, token, int((24 * time.Hour).Seconds()), "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"data": user})
}

// requireLogin 校验会话 cookie，未登录返回 401。
func (h *AuthHandler) requireLogin(c *gin.Context) {
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil || cookie == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		c.Abort()
		return
	}
	username, displayName, ok := h.auth.VerifySession(cookie)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "会话已失效，请重新登录"})
		c.Abort()
		return
	}
	c.Set("admin_username", username)
	c.Set("admin_display", displayName)
	c.Next()
}

// authRoutes 注册认证相关路由（登录/登出/当前用户/改密）。
// 这些路由挂在 /api/admin 下，登录与登出本身不需要鉴权。
func (h *AuthHandler) authRoutes(r *gin.RouterGroup) {
	r.POST("/login", h.login)
	r.POST("/logout", h.logout)
	// 以下需要登录
	authed := r.Group("")
	authed.Use(h.requireLogin)
	{
		authed.GET("/me", h.currentUser)
		authed.POST("/change-password", h.changePassword)
	}
}

// resolveSessionUser 供 SPA 静态托管判断是否需要引导登录（这里直接复用 requireLogin 逻辑）。
func sessionUserFromCookie(c *gin.Context, auth *service.AuthService) (string, bool) {
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	u, _, ok := auth.VerifySession(cookie)
	return u, ok
}
