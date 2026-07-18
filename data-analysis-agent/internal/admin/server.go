// Package admin 提供数据分析助手的后台管理：登录、配置查看/修改/重置。
// 所有运行配置（LLM / MCP / Agent / 提示词 / 后台凭据）均持久化在数据库，
// 修改后即时热更新到 Agent，无需重启进程。
package admin

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"company.com/data-analysis-agent/agent"
	"company.com/data-analysis-agent/internal/admin/web"
	"company.com/data-analysis-agent/internal/confdb"
	"company.com/data-analysis-agent/internal/userdb"
)

// Server 后台管理服务。
type Server struct {
	store      *confdb.Store
	users      *userdb.Store
	ag         *agent.Agent
	adminToken string // 进程内管理员令牌（登录成功后下发）
	adminUser  *adminSession
}

type adminSession struct {
	ID       string
	Username string
	Role     string
}

// New 构造后台管理服务。
func New(store *confdb.Store, users *userdb.Store, ag *agent.Agent) *Server {
	return &Server{
		store:      store,
		users:      users,
		ag:         ag,
		adminToken: genToken(),
	}
}

// Handler 返回后台 API 与页面路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/login", s.handleLogin)
	mux.HandleFunc("/api/admin/config", s.handleConfig)
	mux.HandleFunc("/api/admin/reset", s.handleReset)
	mux.HandleFunc("/api/admin/users", s.handleUsers)
	mux.HandleFunc("/api/admin/users/import", s.handleUsersImport)
	mux.HandleFunc("/api/admin/users/export", s.handleUsersExport)
	mux.HandleFunc("/api/admin/users/", s.handleUsersSub)
	mux.HandleFunc("/api/admin/roles", s.handleRoles)
	mux.HandleFunc("/api/admin/chat-logs", s.handleChatLogs)
	mux.HandleFunc("/api/admin/llm-logs", s.handleLLMLogs)
	mux.HandleFunc("/api/admin/mcp-logs", s.handleMCPLogs)
	mux.HandleFunc("/api/admin/admins", s.handleAdmins)
	mux.HandleFunc("/api/admin/admins/", s.handleAdminsSub)
	mux.HandleFunc("/api/admin/me", s.handleAdminMe)
	mux.HandleFunc("/admin", s.handlePage)
	mux.HandleFunc("/admin/", s.handlePage)
	return mux
}

// genToken 生成随机管理员令牌。
func genToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "adm_" + hex.EncodeToString(b)
}

// handleLogin 校验后台凭据：优先使用 admins 表，为空时回退到 confdb 默认 admin。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
		return
	}
	// 优先使用用户数据库中的管理员账号
	if s.users != nil {
		admin, err := s.users.AdminLogin(req.Username, req.Password)
		if err == nil && admin != nil {
			s.adminUser = &adminSession{ID: admin.ID, Username: admin.Username, Role: admin.Role}
			writeJSON(w, http.StatusOK, map[string]interface{}{"token": s.adminToken, "admin": map[string]interface{}{"id": admin.ID, "username": admin.Username, "role": admin.Role}})
			return
		}
	}
	// 回退到 confdb 默认管理员
	user, pass := s.store.AdminCreds()
	if req.Username != user || req.Password != pass {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "账号或密码错误"})
		return
	}
	s.adminUser = &adminSession{ID: "default", Username: user, Role: "super_admin"}
	writeJSON(w, http.StatusOK, map[string]interface{}{"token": s.adminToken, "admin": map[string]interface{}{"id": "default", "username": user, "role": "super_admin"}})
}

// requireAuth 校验 Bearer 令牌。
func (s *Server) requireAuth(r *http.Request) bool {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return tok == s.adminToken
}

// handleConfig GET 返回当前全部配置项；PUT 批量更新并热应用到 Agent。
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return
	}
	readPerm := s.hasPermission("config:read")
	writePerm := s.hasPermission("config:write")
	switch r.Method {
	case http.MethodGet:
		if !readPerm {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "当前角色无此操作权限"})
			return
		}
		items, err := s.store.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	case http.MethodPut:
		if !writePerm {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "当前角色无此操作权限"})
			return
		}
		var req struct {
			Values map[string]string `json:"values"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
			return
		}
		if len(req.Values) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "values 不能为空"})
			return
		}
		if err := s.store.Update(req.Values); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		// 热更新到 Agent（LLM / MCP 连接 / 提示词即时生效）。
		if err := s.ag.ApplyConfig(s.store.Get()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error": "配置已保存，但应用到运行实例失败: " + err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "applied": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleReset 重置所有配置为内置默认值。
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return
	}
	if !s.requirePerm(w, r, "config:write") {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.store.Reset(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	if err := s.ag.ApplyConfig(s.store.Get()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "配置已重置，但应用到运行实例失败: " + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "reset": true})
}

// handlePage 返回内嵌的后台管理页面。
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(web.Assets, "assets/index.html")
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// hasPermission 检查当前登录管理员是否拥有指定权限。
// super_admin 默认拥有所有权限；默认管理员也拥有所有权限。
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

// requirePerm 与 requireAuth 结合：先鉴权再校验权限，无权限返回 403。
func (s *Server) requirePerm(w http.ResponseWriter, r *http.Request, perm string) bool {
	if !s.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return false
	}
	if !s.hasPermission(perm) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "当前角色无此操作权限"})
		return false
	}
	return true
}

func (s *Server) usersReady(w http.ResponseWriter) bool {
	if s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "用户管理仅在 HTTP 服务模式下可用"})
		return false
	}
	return true
}

// handleUsers 用户列表 / 批量创建 / 导入。
func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return
	}
	if !s.usersReady(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !s.requirePerm(w, r, "user:read") {
			return
		}
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		size, _ := strconv.Atoi(q.Get("size"))
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
			Search: q.Get("search"),
			Role:   q.Get("role"),
			Page:   page,
			Size:   size,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"users": list, "total": total, "page": page, "size": size,
		})
	case http.MethodPost:
		if !s.requirePerm(w, r, "user:write") {
			return
		}
		var req struct {
			Username string `json:"username"`
			Phone    string `json:"phone"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
			return
		}
		u, err := s.users.AdminCreateUser(req.Username, req.Phone, req.Password, req.Role)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": u.ID, "username": u.Username})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleUsersSub 处理 /api/admin/users/{id}/disable、/password、/role。
func (s *Server) handleUsersSub(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return
	}
	if !s.usersReady(w) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/users/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "缺少用户 ID 或操作"})
		return
	}
	userID := parts[0]
	action := parts[1]
	switch action {
	case "disable":
		if !s.requirePerm(w, r, "user:write") {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Disabled bool `json:"disabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := s.users.SetUserDisabled(userID, body.Disabled); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "password":
		if !s.requirePerm(w, r, "user:write") {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
			return
		}
		if err := s.users.SetUserPassword(userID, body.Password); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "role":
		if !s.requirePerm(w, r, "user:write") {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
			return
		}
		if err := s.users.SetUserRole(userID, body.Role); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "未知操作"})
	}
}

// writeJSON 统一写 JSON 响应。
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// handleRoles 角色列表 / 新增 / 更新 / 删除。
func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return
	}
	if !s.usersReady(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !s.hasPermission("role:read") {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "当前角色无此操作权限"})
			return
		}
		roles, err := s.users.ListRoles()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"roles": roles})
	case http.MethodPost:
		if !s.hasPermission("role:write") {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "当前角色无此操作权限"})
			return
		}
		var req userdb.Role
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
			return
		}
		if err := s.users.UpsertRole(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	case http.MethodDelete:
		if !s.hasPermission("role:delete") {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "当前角色无此操作权限"})
			return
		}
		name := r.URL.Query().Get("name")
		if err := s.users.DeleteRole(name); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleChatLogs 沟通日志查询。
func (s *Server) handleChatLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requirePerm(w, r, "chat_log:read") {
		return
	}
	if !s.usersReady(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
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
		Username: q.Get("username"),
		Role:     q.Get("role"),
		Keyword:  q.Get("keyword"),
		DateFrom: q.Get("date_from"),
		DateTo:   q.Get("date_to"),
		Page:     page,
		Size:     size,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs": list, "total": total, "page": page, "size": size,
	})
}

// handleUsersImport 处理用户 CSV 导入（POST /api/admin/users/import）。
func (s *Server) handleUsersImport(w http.ResponseWriter, r *http.Request) {
	if !s.requirePerm(w, r, "user:write") {
		return
	}
	if !s.usersReady(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "缺少 file 字段"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	n, err := s.users.ImportUsersCSV(bytes.NewBuffer(data))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"imported": n})
}

// handleUsersExport 导出用户 CSV（不含密码）。
func (s *Server) handleUsersExport(w http.ResponseWriter, r *http.Request) {
	if !s.requirePerm(w, r, "user:read") {
		return
	}
	if !s.usersReady(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := s.users.ExportUsersCSV()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=users.csv")
	_, _ = w.Write(data)
}

// handleLLMLogs LLM 调用日志查询。
func (s *Server) handleLLMLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requirePerm(w, r, "llm_log:read") {
		return
	}
	if !s.usersReady(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
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
		Username: q.Get("username"),
		Model:    q.Get("model"),
		Keyword:  q.Get("keyword"),
		DateFrom: q.Get("date_from"),
		DateTo:   q.Get("date_to"),
		Page:     page,
		Size:     size,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs": list, "total": total, "page": page, "size": size,
	})
}

// handleMCPLogs MCP 调用日志查询。
func (s *Server) handleMCPLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requirePerm(w, r, "mcp_log:read") {
		return
	}
	if !s.usersReady(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
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
		Username: q.Get("username"),
		ToolName: q.Get("tool_name"),
		Keyword:  q.Get("keyword"),
		DateFrom: q.Get("date_from"),
		DateTo:   q.Get("date_to"),
		Page:     page,
		Size:     size,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs": list, "total": total, "page": page, "size": size,
	})
}

// handleAdminMe 返回当前登录管理员信息。
func (s *Server) handleAdminMe(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return
	}
	if s.adminUser == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       s.adminUser.ID,
		"username": s.adminUser.Username,
		"role":     s.adminUser.Role,
	})
}

// handleAdmins 管理员账号列表 / 新增。
func (s *Server) handleAdmins(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return
	}
	if !s.usersReady(w) {
		return
	}
	if !s.hasPermission("admin:manage") {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "当前角色无此操作权限"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		admins, err := s.users.ListAdmins()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"admins": admins})
	case http.MethodPost:
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
			return
		}
		a, err := s.users.CreateAdmin(req.Username, req.Password, req.Role)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"id": a.ID, "username": a.Username})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAdminsSub 处理 /api/admin/admins/{id}/disable、/password、/role、DELETE。
func (s *Server) handleAdminsSub(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "未授权，请先登录"})
		return
	}
	if !s.usersReady(w) {
		return
	}
	if !s.hasPermission("admin:manage") {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "当前角色无此操作权限"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/admins/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "缺少管理员 ID"})
		return
	}
	adminID := parts[0]
	action := ""
	if len(parts) >= 2 {
		action = parts[1]
	}
	if action == "" && r.Method == http.MethodDelete {
		if err := s.users.DeleteAdmin(adminID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
		return
	}
	switch action {
	case "disable":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Disabled bool `json:"disabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := s.users.SetAdminDisabled(adminID, body.Disabled); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "password":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
			return
		}
		if err := s.users.SetAdminPassword(adminID, body.Password); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "role":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "请求体解析失败"})
			return
		}
	if err := s.users.SetAdminRole(adminID, body.Role); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
default:
	writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "未知操作"})
}
}
