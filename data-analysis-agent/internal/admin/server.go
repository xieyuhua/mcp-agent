// Package admin 提供数据分析助手的后台管理：登录、配置查看/修改/重置、用户/角色/管理员/权限管理。
// 所有运行配置（LLM / MCP / Agent / 提示词 / 后台凭据）均持久化在数据库，
// 修改后即时热更新到 Agent，无需重启进程。
package admin

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"company.com/data-analysis-agent/agent"
	"company.com/data-analysis-agent/internal/admin/web"
	"company.com/data-analysis-agent/internal/confdb"
	"company.com/data-analysis-agent/internal/userdb"
	"company.com/data-analysis-agent/mcpclient"

	"github.com/gin-gonic/gin"
)

// 后台权限清单（也用于前端复选框展示）。
var AdminPermissions = []Permission{
	{Code: "config:read", Name: "配置查看", Module: "系统配置"},
	{Code: "config:write", Name: "配置修改", Module: "系统配置"},
	{Code: "user:read", Name: "用户查看", Module: "用户管理"},
	{Code: "user:write", Name: "用户新增/编辑", Module: "用户管理"},
	{Code: "user:delete", Name: "用户删除", Module: "用户管理"},
	{Code: "role:read", Name: "角色查看", Module: "角色管理"},
	{Code: "role:write", Name: "角色新增/编辑", Module: "角色管理"},
	{Code: "role:delete", Name: "角色删除", Module: "角色管理"},
	{Code: "admin:read", Name: "管理员查看", Module: "管理员管理"},
	{Code: "admin:write", Name: "管理员新增/编辑", Module: "管理员管理"},
	{Code: "admin:delete", Name: "管理员删除", Module: "管理员管理"},
	{Code: "admin_role:read", Name: "管理员角色查看", Module: "权限管理"},
	{Code: "admin_role:write", Name: "管理员角色新增/编辑", Module: "权限管理"},
	{Code: "admin_role:delete", Name: "管理员角色删除", Module: "权限管理"},
	{Code: "chat_log:read", Name: "沟通日志查看", Module: "日志管理"},
	{Code: "llm_log:read", Name: "LLM 日志查看", Module: "日志管理"},
	{Code: "mcp_log:read", Name: "MCP 日志查看", Module: "日志管理"},
}

// Permission 描述一项可分配的权限。
type Permission struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Module string `json:"module"`
}

// Server 后台管理服务。
type Server struct {
	store      *confdb.Store
	users      *userdb.Store
	ag         *agent.Agent
	sessions   map[string]*adminSession // token -> session
	sessionMu sync.RWMutex
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
		store:     store,
		users:     users,
		ag:        ag,
		sessions:  make(map[string]*adminSession),
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
		api.POST("/logout", s.requireAuth(), s.handleLogout)
		api.GET("/me", s.requireAuth(), s.handleAdminMe)
		api.POST("/me/password", s.requireAuth(), s.handleAdminChangePassword)

		api.GET("/permissions", s.requireAuth(), s.handlePermissions)

		api.GET("/config", s.requireAuth(), s.requirePerm("config:read"), s.handleConfig)
		api.PUT("/config", s.requireAuth(), s.requirePerm("config:write"), s.handleConfig)
		api.POST("/reset", s.requireAuth(), s.requirePerm("config:write"), s.handleReset)
		api.POST("/mcp-test", s.requireAuth(), s.requirePerm("config:read"), s.handleMCPTest)

		api.GET("/users", s.requireAuth(), s.requirePerm("user:read"), s.handleUsers)
		api.POST("/users", s.requireAuth(), s.requirePerm("user:write"), s.handleUsers)
		api.POST("/users/import", s.requireAuth(), s.requirePerm("user:write"), s.handleUsersImport)
		api.GET("/users/export", s.requireAuth(), s.requirePerm("user:read"), s.handleUsersExport)
		api.POST("/users/:id/disable", s.requireAuth(), s.requirePerm("user:write"), s.handleUserDisable)
		api.POST("/users/:id/password", s.requireAuth(), s.requirePerm("user:write"), s.handleUserPassword)
		api.POST("/users/:id/role", s.requireAuth(), s.requirePerm("user:write"), s.handleUserRole)
		api.DELETE("/users/:id", s.requireAuth(), s.requirePerm("user:delete"), s.handleUserDelete)

		api.GET("/roles", s.requireAuth(), s.requirePerm("role:read"), s.handleRoles)
		api.POST("/roles", s.requireAuth(), s.requirePerm("role:write"), s.handleRoles)
		api.DELETE("/roles", s.requireAuth(), s.requirePerm("role:delete"), s.handleRoleDelete)

		api.GET("/admins", s.requireAuth(), s.requirePerm("admin:read"), s.handleAdmins)
		api.POST("/admins", s.requireAuth(), s.requirePerm("admin:write"), s.handleAdmins)
		api.DELETE("/admins/:id", s.requireAuth(), s.requirePerm("admin:delete"), s.handleAdminDelete)
		api.POST("/admins/:id/disable", s.requireAuth(), s.requirePerm("admin:write"), s.handleAdminDisable)
		api.POST("/admins/:id/password", s.requireAuth(), s.requirePerm("admin:write"), s.handleAdminPassword)
		api.POST("/admins/:id/role", s.requireAuth(), s.requirePerm("admin:write"), s.handleAdminRole)

		api.GET("/admin-roles", s.requireAuth(), s.requirePerm("admin_role:read"), s.handleAdminRoles)
		api.POST("/admin-roles", s.requireAuth(), s.requirePerm("admin_role:write"), s.handleAdminRoles)
		api.DELETE("/admin-roles", s.requireAuth(), s.requirePerm("admin_role:delete"), s.handleAdminRoleDelete)

		api.GET("/chat-logs", s.requireAuth(), s.requirePerm("chat_log:read"), s.handleChatLogs)
		api.GET("/llm-logs", s.requireAuth(), s.requirePerm("llm_log:read"), s.handleLLMLogs)
		api.GET("/mcp-logs", s.requireAuth(), s.requirePerm("mcp_log:read"), s.handleMCPLogs)
	}

	// Vue 后台管理构建产物：统一通过 /admin 和 /admin/* 提供，找不到的文件回退到 index.html。
	if distFS, err := fs.Sub(web.Assets, "dist"); err == nil {
		serveFile := func(path string) ([]byte, string, error) {
			path = strings.TrimPrefix(path, "/")
			if path == "" {
				path = "index.html"
			}
			if strings.Contains(path, "..") {
				path = "index.html"
			}
			data, err := fs.ReadFile(distFS, path)
			if err != nil {
				data, err = fs.ReadFile(distFS, "index.html")
				if err != nil {
					return nil, "", err
				}
				return data, "text/html; charset=utf-8", nil
			}
			contentType := "application/octet-stream"
			switch {
			case strings.HasSuffix(path, ".js"):
				contentType = "application/javascript"
			case strings.HasSuffix(path, ".css"):
				contentType = "text/css"
			case strings.HasSuffix(path, ".html"):
				contentType = "text/html; charset=utf-8"
			case strings.HasSuffix(path, ".png"):
				contentType = "image/png"
			case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
				contentType = "image/jpeg"
			case strings.HasSuffix(path, ".svg"):
				contentType = "image/svg+xml"
			case strings.HasSuffix(path, ".json"):
				contentType = "application/json"
			}
			return data, contentType, nil
		}
		adminHandler := func(c *gin.Context) {
			data, ct, err := serveFile(c.Param("path"))
			if err != nil {
				c.String(http.StatusNotFound, "page not found")
				return
			}
			if strings.HasSuffix(ct, "html") {
				c.Header("Cache-Control", "no-store")
			} else {
				c.Header("Cache-Control", "public, max-age=86400")
			}
			c.Data(http.StatusOK, ct, data)
		}
		r.GET("/admin", adminHandler)
		r.GET("/admin/*path", adminHandler)
	}
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
		if tok == "" {
			if cookie, err := c.Cookie("admin_token"); err == nil {
				tok = cookie
			}
		}
		s.sessionMu.RLock()
		sess, ok := s.sessions[tok]
		s.sessionMu.RUnlock()
		if !ok || sess == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权，请先登录"})
			c.Abort()
			return
		}
		c.Set("admin_session", sess)
		c.Set("admin_token", tok)
		c.Next()
	}
}

func (s *Server) requirePerm(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		sess, _ := c.Get("admin_session")
		as, ok := sess.(*adminSession)
		if !ok || as == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权，请先登录"})
			c.Abort()
			return
		}
		if !s.hasPermission(as, perm) {
			c.JSON(http.StatusForbidden, gin.H{"error": "当前角色无此操作权限"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// hasPermission 检查管理员是否拥有指定权限。
func (s *Server) hasPermission(as *adminSession, perm string) bool {
	if as == nil {
		return false
	}
	if as.Role == "super_admin" || as.ID == "default" {
		return true
	}
	if s.users == nil {
		return false
	}
	return containsPerm(s.users.AdminRolePermissions(as.Role), perm)
}

func containsPerm(perms []string, perm string) bool {
	if containsStr(perms, "admin:all") {
		return true
	}
	return containsStr(perms, perm)
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if strings.TrimSpace(x) == v {
			return true
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

func sessionFromCtx(c *gin.Context) *adminSession {
	v, _ := c.Get("admin_session")
	if s, ok := v.(*adminSession); ok {
		return s
	}
	return nil
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
			tok := genToken()
			sess := &adminSession{ID: admin.ID, Username: admin.Username, Role: admin.Role}
			s.sessionMu.Lock()
			s.sessions[tok] = sess
			s.sessionMu.Unlock()
			c.SetCookie("admin_token", tok, 86400, "/", "", false, true)
			c.JSON(http.StatusOK, gin.H{
				"token": tok,
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
	tok := genToken()
	sess := &adminSession{ID: "default", Username: user, Role: "super_admin"}
	s.sessionMu.Lock()
	s.sessions[tok] = sess
	s.sessionMu.Unlock()
	c.SetCookie("admin_token", tok, 86400, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{
		"token": tok,
		"admin": gin.H{"id": "default", "username": user, "role": "super_admin"},
	})
}

func (s *Server) handleLogout(c *gin.Context) {
	tok, _ := c.Get("admin_token")
	if t, ok := tok.(string); ok && t != "" {
		s.sessionMu.Lock()
		delete(s.sessions, t)
		s.sessionMu.Unlock()
	}
	c.SetCookie("admin_token", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleAdminMe(c *gin.Context) {
	as := sessionFromCtx(c)
	if as == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权，请先登录"})
		return
	}
	perms := []string{}
	if as.Role == "super_admin" || as.ID == "default" {
		perms = []string{"admin:all"}
	} else if s.users != nil {
		perms = s.users.AdminRolePermissions(as.Role)
	}
	c.JSON(http.StatusOK, gin.H{
		"id":          as.ID,
		"username":    as.Username,
		"role":        as.Role,
		"permissions": perms,
	})
}

func (s *Server) handlePermissions(c *gin.Context) {
	as := sessionFromCtx(c)
	if as == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权，请先登录"})
		return
	}
	modules := map[string][]Permission{}
	for _, p := range AdminPermissions {
		modules[p.Module] = append(modules[p.Module], p)
	}
	myPerms := []string{"admin:all"}
	if as.Role != "super_admin" && as.ID != "default" && s.users != nil {
		myPerms = s.users.AdminRolePermissions(as.Role)
	}
	c.JSON(http.StatusOK, gin.H{
		"permissions": AdminPermissions,
		"modules":     modules,
		"mine":        myPerms,
	})
}

// handleAdminChangePassword 修改当前登录管理员自己的密码。
func (s *Server) handleAdminChangePassword(c *gin.Context) {
	as := sessionFromCtx(c)
	if as == nil {
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
	if as.ID == "default" {
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
	// 校验原密码，避免任意管理员免密改他人/自己密码。
	if _, err := s.users.AdminLogin(as.Username, body.OldPassword); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "原密码错误"})
		return
	}
	if err := s.users.SetAdminPassword(as.ID, body.NewPassword); err != nil {
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

// handleMCPTest 后端实测远程 MCP 连接（同源，不受浏览器 CORS 限制）。
// 可选传入 base_url/transport/api_key 覆盖当前配置，便于“先测试再保存”。
// 复用与运行时一致的 mcpclient，确保测试结果等价于实际对接效果。
func (s *Server) handleMCPTest(c *gin.Context) {
	var req struct {
		BaseURL   string            `json:"base_url"`
		Transport string            `json:"transport"`
		APIKey    string            `json:"api_key"`
		Headers   map[string]string `json:"headers"`
	}
	_ = c.ShouldBindJSON(&req)

	rc := s.ag.MCPRemoteConfig()
	if req.BaseURL != "" {
		rc.BaseURL = req.BaseURL
	}
	if req.Transport != "" {
		rc.Transport = req.Transport
	}
	if req.APIKey != "" {
		rc.APIKey = req.APIKey
	}
	if len(req.Headers) > 0 {
		rc.Headers = req.Headers
	}
	if rc.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "未配置或未传入远程 MCP 地址 (mcp.base_url)"})
		return
	}
	cli, err := mcpclient.StartRemote(rc)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	defer cli.Close()
	tools := cli.Tools()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "tool_count": len(names), "tools": names})
}

// handlePage 返回内嵌的后台管理页面。
func (s *Server) handlePage(c *gin.Context) {
	data, err := web.Assets.ReadFile("dist/index.html")
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

func (s *Server) handleUserDelete(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	if err := s.users.DeleteUser(c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
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

// ---- 管理员角色 / 权限 ----

func (s *Server) handleAdminRoles(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		roles, err := s.users.ListAdminRoles()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"roles": roles})
	case http.MethodPost:
		var req userdb.AdminRole
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		if err := s.users.UpsertAdminRole(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "方法不支持"})
	}
}

func (s *Server) handleAdminRoleDelete(c *gin.Context) {
	if !s.usersReady(c) {
		return
	}
	name := c.Query("name")
	if err := s.users.DeleteAdminRole(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
