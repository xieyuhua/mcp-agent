// Package admin 提供数据分析助手的后台管理：登录、配置查看/修改/重置。
// 所有运行配置（LLM / MCP / Agent / 提示词 / 后台凭据）均持久化在数据库，
// 修改后即时热更新到 Agent，无需重启进程。
package admin

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"

	"company.com/data-analysis-agent/agent"
	"company.com/data-analysis-agent/internal/admin/web"
	"company.com/data-analysis-agent/internal/confdb"
	"company.com/data-analysis-agent/internal/userdb"

	"github.com/gin-gonic/gin"
)

// Server 后台管理服务。
type Server struct {
	store      *confdb.Store
	users      *userdb.Store
	ag         *agent.Agent
	adminToken string // 进程内管理员令牌（登录成功后下发）
	adminUser  *adminSession
	router     *gin.Engine
}

type adminSession struct {
	ID       string
	Username string
	Role     string
}

// New 构造后台管理服务。
func New(store *confdb.Store, users *userdb.Store, ag *agent.Agent) *Server {
	s := &Server{
		store:      store,
		users:      users,
		ag:         ag,
		adminToken: genToken(),
	}
	s.router = s.buildRouter()
	return s
}

// Handler 返回后台 API 与页面路由（http.Handler 兼容）。
func (s *Server) Handler() http.Handler {
	return s.router
}

// buildRouter 配置 Gin 路由。
func (s *Server) buildRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api/admin")
	{
		api.POST("/login", s.handleLogin)
		api.GET("/me", s.requireAuth(), s.handleAdminMe)
		api.POST("/me/password", s.requireAuth(), s.handleAdminChangePassword)
		api.GET("/config", s.requireAuth(), s.requirePerm("config:read"), s.handleConfig)
		api.PUT("/config", s.requireAuth(), s.requirePerm("config:write"), s.handleConfig)
		api.POST("/reset", s.requireAuth(), s.requirePerm("config:write"), s.handleReset)

		api.GET("/users", s.requireAuth(), s.requirePerm("user:read"), s.handleUsers)
		api.POST("/users", s.requireAuth(), s.requirePerm("user:write"), s.handleUsers)
		api.POST("/users/import", s.requireAuth(), s.requirePerm("user:write"), s.handleUsersImport)
		api.GET("/users/export", s.requireAuth(), s.requirePerm("user:read"), s.handleUsersExport)
		api.POST("/users/:id/disable", s.requireAuth(), s.requirePerm("user:write"), s.handleUserDisable)
		api.POST("/users/:id/password", s.requireAuth(), s.requirePerm("user:write"), s.handleUserPassword)
		api.POST("/users/:id/role", s.requireAuth(), s.requirePerm("user:write"), s.handleUserRole)

		api.GET("/roles", s.requireAuth(), s.requirePerm("role:read"), s.handleRoles)
		api.POST("/roles", s.requireAuth(), s.requirePerm("role:write"), s.handleRoles)
		api.DELETE("/roles", s.requireAuth(), s.requirePerm("role:delete"), s.handleRoleDelete)

		api.GET("/chat-logs", s.requireAuth(), s.requirePerm("chat_log:read"), s.handleChatLogs)
		api.GET("/llm-logs", s.requireAuth(), s.requirePerm("llm_log:read"), s.handleLLMLogs)
		api.GET("/mcp-logs", s.requireAuth(), s.requirePerm("mcp_log:read"), s.handleMCPLogs)

		api.GET("/admins", s.requireAuth(), s.requirePerm("admin:manage"), s.handleAdmins)
		api.POST("/admins", s.requireAuth(), s.requirePerm("admin:manage"), s.handleAdmins)
		api.DELETE("/admins/:id", s.requireAuth(), s.requirePerm("admin:manage"), s.handleAdminDelete)
		api.POST("/admins/:id/disable", s.requireAuth(), s.requirePerm("admin:manage"), s.handleAdminDisable)
		api.POST("/admins/:id/password", s.requireAuth(), s.requirePerm("admin:manage"), s.handleAdminPassword)
		api.POST("/admins/:id/role", s.requireAuth(), s.requirePerm("admin:manage"), s.handleAdminRole)
	}

	r.GET("/admin", s.handlePage)
	r.GET("/admin/*path", s.handlePage)
	return r
}

// genToken 生成随机管理员令牌。
func genToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "adm_" + hex.EncodeToString(b)
}

// ---- 中间件 ----

func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if tok != s.adminToken {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权，请先登录"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) requirePerm(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.hasPermission(perm) {
			c.JSON(http.StatusForbidden, gin.H{"error": "当前角色无此操作权限"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// hasPermission 检查当前登录管理员是否拥有指定权限。
func (s *Server) hasPermission(perm string) bool {
	if s.adminUser == nil {
		return false
	}
	if s.adminUser.Role == "super_admin" || s.adminUser.ID == "default" {
		return true
	}
	roles, err := s.users.ListRoles()
	if err != nil {
		return false
	}
	for _, r := range roles {
		if r.Name == s.adminUser.Role {
			return strings.Contains(r.Permissions, perm) || strings.Contains(r.Permissions, "admin:all")
		}
	}
	return false
}

func (s *Server) usersReady(c *gin.Context) bool {
	if s.users == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "用户管理仅在 HTTP 服务模式下可用"})
		return false
	}
	return true
}

// ---- 登录 / 当前管理员 ----

// handleLogin 校验后台凭据：优先使用 admins 表，为空时回退到 confdb 默认 admin。
func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
		return
	}
	if s.users != nil {
		admin, err := s.users.AdminLogin(req.Username, req.Password)
		if err == nil && admin != nil {
			s.adminUser = &adminSession{ID: admin.ID, Username: admin.Username, Role: admin.Role}
			c.JSON(http.StatusOK, gin.H{
				"token": s.adminToken,
				"admin": gin.H{"id": admin.ID, "username": admin.Username, "role": admin.Role},
			})
			return
		}
	}
	user, pass := s.store.AdminCreds()
	if req.Username != user || req.Password != pass {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "账号或密码错误"})
		return
	}
	s.adminUser = &adminSession{ID: "default", Username: user, Role: "super_admin"}
	c.JSON(http.StatusOK, gin.H{
		"token": s.adminToken,
		"admin": gin.H{"id": "default", "username": user, "role": "super_admin"},
	})
}

func (s *Server) handleAdminMe(c *gin.Context) {
	if s.adminUser == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权，请先登录"})
		return
	}
	perms := []string{}
	if s.adminUser.Role == "super_admin" || s.adminUser.ID == "default" {
		perms = []string{"admin:all"}
	} else if s.users != nil {
		roles, err := s.users.ListRoles()
		if err == nil {
			for _, r := range roles {
				if r.Name == s.adminUser.Role {
					for _, p := range strings.Split(r.Permissions, ",") {
						p = strings.TrimSpace(p)
						if p != "" {
							perms = append(perms, p)
						}
					}
					break
				}
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"id":          s.adminUser.ID,
		"username":    s.adminUser.Username,
		"role":        s.adminUser.Role,
		"permissions": perms,
	})
}

// handleAdminChangePassword 修改当前登录管理员自己的密码。
func (s *Server) handleAdminChangePassword(c *gin.Context) {
	if s.adminUser == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权，请先登录"})
		return
	}
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
		return
	}
	if s.adminUser.ID == "default" {
		_, pass := s.store.AdminCreds()
		if body.OldPassword != pass {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "原密码错误"})
			return
		}
		if err := s.store.Update(map[string]string{confdb.KeyAdminPass: body.NewPassword}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	if !s.usersReady(c) {
		return
	}
	if err := s.users.SetAdminPassword(s.adminUser.ID, body.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- 配置 ----

func (s *Server) handleConfig(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet:
		items, err := s.store.List()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	case http.MethodPut:
		var req struct {
			Values map[string]string `json:"values"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		if len(req.Values) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "values 不能为空"})
			return
		}
		if err := s.store.Update(req.Values); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := s.ag.ApplyConfig(s.store.Get()); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "配置已保存，但应用到运行实例失败: " + err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "applied": true})
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "方法不支持"})
	}
}

func (s *Server) handleReset(c *gin.Context) {
	if err := s.store.Reset(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := s.ag.ApplyConfig(s.store.Get()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "配置已重置，但应用到运行实例失败: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "reset": true})
}

// handlePage 返回内嵌的后台管理页面。
func (s *Server) handlePage(c *gin.Context) {
	data, err := web.Assets.ReadFile("assets/index.html")
	if err != nil {
		c.String(http.StatusNotFound, "page not found")
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, string(data))
}

// ---- 用户管理 ----

func (s *Server) handleUsers(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		page, _ := strconv.Atoi(c.Query("page"))
		size, _ := strconv.Atoi(c.Query("size"))
		defaultSize := s.ag.UIConfig().AdminPageSize
		if defaultSize <= 0 || defaultSize > 200 {
			defaultSize = 20
		}
		if page < 1 {
			page = 1
		}
		if size <= 0 || size > 200 {
			size = defaultSize
		}
		list, total, err := s.users.ListUsers(userdb.ListUsersRequest{
			Search: c.Query("search"),
			Role:   c.Query("role"),
			Page:   page,
			Size:   size,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"users": list, "total": total, "page": page, "size": size})
	case http.MethodPost:
		var req struct {
			Username string `json:"username"`
			Phone    string `json:"phone"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		u, err := s.users.AdminCreateUser(req.Username, req.Phone, req.Password, req.Role)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": u.ID, "username": u.Username})
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "方法不支持"})
	}
}

func (s *Server) handleUserDisable(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	var body struct {
		Disabled bool `json:"disabled"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := s.users.SetUserDisabled(c.Param("id"), body.Disabled); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleUserPassword(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
		return
	}
	if err := s.users.SetUserPassword(c.Param("id"), body.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleUserRole(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
		return
	}
	if err := s.users.SetUserRole(c.Param("id"), body.Role); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleUsersImport(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 file 字段"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	n, err := s.users.ImportUsersCSV(bytes.NewBuffer(data))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"imported": n})
}

func (s *Server) handleUsersExport(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	data, err := s.users.ExportUsersCSV()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=users.csv")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
}

// ---- 角色 ----

func (s *Server) handleRoles(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		roles, err := s.users.ListRoles()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"roles": roles})
	case http.MethodPost:
		var req userdb.Role
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		if err := s.users.UpsertRole(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "方法不支持"})
	}
}

func (s *Server) handleRoleDelete(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	name := c.Query("name")
	if err := s.users.DeleteRole(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- 日志 ----

func (s *Server) handleChatLogs(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("size"))
	defaultSize := s.ag.UIConfig().AdminPageSize
	if defaultSize <= 0 || defaultSize > 200 {
		defaultSize = 20
	}
	if page < 1 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = defaultSize
	}
	list, total, err := s.users.ListChatLogs(userdb.ChatLogFilter{
		Username: c.Query("username"),
		Role:     c.Query("role"),
		Keyword:  c.Query("keyword"),
		DateFrom: c.Query("date_from"),
		DateTo:   c.Query("date_to"),
		Page:     page,
		Size:     size,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": list, "total": total, "page": page, "size": size})
}

func (s *Server) handleLLMLogs(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("size"))
	defaultSize := s.ag.UIConfig().AdminPageSize
	if defaultSize <= 0 || defaultSize > 200 {
		defaultSize = 20
	}
	if page < 1 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = defaultSize
	}
	list, total, err := s.users.ListLLMCallLogs(userdb.LLMLogFilter{
		Username: c.Query("username"),
		Model:    c.Query("model"),
		Keyword:  c.Query("keyword"),
		DateFrom: c.Query("date_from"),
		DateTo:   c.Query("date_to"),
		Page:     page,
		Size:     size,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": list, "total": total, "page": page, "size": size})
}

func (s *Server) handleMCPLogs(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("size"))
	defaultSize := s.ag.UIConfig().AdminPageSize
	if defaultSize <= 0 || defaultSize > 200 {
		defaultSize = 20
	}
	if page < 1 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = defaultSize
	}
	list, total, err := s.users.ListMCPCallLogs(userdb.MCPLogFilter{
		Username: c.Query("username"),
		ToolName: c.Query("tool_name"),
		Keyword:  c.Query("keyword"),
		DateFrom: c.Query("date_from"),
		DateTo:   c.Query("date_to"),
		Page:     page,
		Size:     size,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"logs": list, "total": total, "page": page, "size": size})
}

// ---- 管理员 ----

func (s *Server) handleAdmins(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		admins, err := s.users.ListAdmins()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"admins": admins})
	case http.MethodPost:
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		a, err := s.users.CreateAdmin(req.Username, req.Password, req.Role)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": a.ID, "username": a.Username})
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "方法不支持"})
	}
}

func (s *Server) handleAdminDelete(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	if err := s.users.DeleteAdmin(c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleAdminDisable(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	var body struct {
		Disabled bool `json:"disabled"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := s.users.SetAdminDisabled(c.Param("id"), body.Disabled); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleAdminPassword(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
		return
	}
	if err := s.users.SetAdminPassword(c.Param("id"), body.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleAdminRole(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
		return
	}
	if err := s.users.SetAdminRole(c.Param("id"), body.Role); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
