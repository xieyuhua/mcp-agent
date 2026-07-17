// Package admin 提供数据分析助手的后台管理：登录、配置查看/修改/重置。
// 所有运行配置（LLM / MCP / Agent / 提示词 / 后台凭据）均持久化在数据库，
// 修改后即时热更新到 Agent，无需重启进程。
package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"company.com/data-analysis-agent/agent"
	"company.com/data-analysis-agent/internal/admin/web"
	"company.com/data-analysis-agent/internal/confdb"
)

// Server 后台管理服务。
type Server struct {
	store      *confdb.Store
	ag         *agent.Agent
	adminToken string // 进程内管理员令牌（登录成功后下发）
}

// New 构造后台管理服务。
func New(store *confdb.Store, ag *agent.Agent) *Server {
	return &Server{
		store:      store,
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

// handleLogin 校验后台凭据，成功返回令牌。
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
	user, pass := s.store.AdminCreds()
	if req.Username != user || req.Password != pass {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "账号或密码错误"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"token": s.adminToken})
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
	switch r.Method {
	case http.MethodGet:
		items := s.store.List()
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	case http.MethodPut:
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

// writeJSON 统一 JSON 输出。
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
