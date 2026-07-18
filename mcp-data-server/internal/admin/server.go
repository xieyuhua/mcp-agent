package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"company.com/mcp-data-server/internal/service"
	"company.com/mcp-data-server/internal/tenant"
)

// Server 权限可视化后台服务：REST API 用于配置角色策略与脱敏规则。
// 仅 super_admin 可访问（令牌复用 MCP 的 auth_login 令牌）。
type Server struct {
	auth *service.AuthService
	perm *service.PermissionService
}

// New 构造后台服务。
func New(authSvc *service.AuthService, permSvc *service.PermissionService) *Server {
	return &Server{auth: authSvc, perm: permSvc}
}

// Handler 返回后台 API 路由（含 /api/admin 前缀）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/login", s.handleLogin)
	mux.HandleFunc("/api/admin/roles", s.handleRoles)
	mux.HandleFunc("/api/admin/policies", s.handlePolicies)
	mux.HandleFunc("/api/admin/policies/export", s.handleExportPolicies)
	mux.HandleFunc("/api/admin/policies/import", s.handleImportPolicies)
	mux.HandleFunc("/api/admin/mask-rules", s.handleMaskRules)
	mux.HandleFunc("/api/admin/mask-rules/export", s.handleExportMaskRules)
	mux.HandleFunc("/api/admin/mask-rules/import", s.handleImportMaskRules)
	mux.HandleFunc("/api/admin/field-permissions", s.handleFieldPermissions)
	mux.HandleFunc("/api/admin/field-permissions/export", s.handleExportFieldPermissions)
	mux.HandleFunc("/api/admin/field-permissions/import", s.handleImportFieldPermissions)
	return mux
}

// ctxFromRequest 校验 Bearer/Cookie 令牌，返回 super_admin 租户上下文。
func (s *Server) ctxFromRequest(r *http.Request) (*tenant.Context, error) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		if c, err := r.Cookie("token"); err == nil {
			tok = c.Value
		}
	}
	if tok == "" {
		return nil, fmt.Errorf("missing token")
	}
	tc, err := s.auth.VerifyToken(tok)
	if err != nil {
		return nil, err
	}
	if tc.Role != "super_admin" {
		return nil, fmt.Errorf("only super_admin can access admin api")
	}
	return tc, nil
}

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
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid body"})
		return
	}
	tok, _, err := s.auth.Login(req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"token": tok})
}

func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request) {
	tc, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	switch r.Method {
	case http.MethodGet:
		roles, err := s.perm.ListRoles(tc, r.URL.Query().Get("tenant_id"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"roles": roles})
	case http.MethodPost:
		var req service.SetRoleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid body"})
			return
		}
		v, err := s.perm.SetRole(tc, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		if err := s.perm.DeleteRole(tc, r.URL.Query().Get("tenant_id"), r.URL.Query().Get("name")); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePolicies(w http.ResponseWriter, r *http.Request) {
	tc, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	switch r.Method {
	case http.MethodGet:
		views, err := s.perm.ListPolicies(tc, r.URL.Query().Get("tenant_id"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"policies": views})
	case http.MethodPost:
		var req service.SetPolicyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid body"})
			return
		}
		v, err := s.perm.SetPolicy(tc, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		if err := s.perm.DeletePolicy(tc, r.URL.Query().Get("tenant_id"), r.URL.Query().Get("role")); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMaskRules(w http.ResponseWriter, r *http.Request) {
	tc, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	switch r.Method {
	case http.MethodGet:
		rules, err := s.perm.ListMaskRules(tc, r.URL.Query().Get("tenant_id"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"rules": rules})
	case http.MethodPost:
		var req service.SetMaskRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid body"})
			return
		}
		v, err := s.perm.SetMaskRule(tc, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		if err := s.perm.DeleteMaskRule(tc, r.URL.Query().Get("tenant_id"), r.URL.Query().Get("table"), r.URL.Query().Get("column")); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFieldPermissions(w http.ResponseWriter, r *http.Request) {
	tc, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	switch r.Method {
	case http.MethodGet:
		views, err := s.perm.ListFieldPermissions(tc, r.URL.Query().Get("tenant_id"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"field_permissions": views})
	case http.MethodPost:
		var req service.SetFieldPermissionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid body"})
			return
		}
		v, err := s.perm.SetFieldPermission(tc, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		if err := s.perm.DeleteFieldPermission(tc,
			r.URL.Query().Get("tenant_id"),
			r.URL.Query().Get("role"),
			r.URL.Query().Get("table"),
			r.URL.Query().Get("column")); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleExportPolicies(w http.ResponseWriter, r *http.Request) {
	_, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := s.perm.ExportPoliciesCSV(r.URL.Query().Get("tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=policies.csv")
	_, _ = w.Write(data)
}

func (s *Server) handleImportPolicies(w http.ResponseWriter, r *http.Request) {
	tc, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "missing file field"})
		return
	}
	defer file.Close()
	n, err := s.perm.ImportPoliciesCSV(tc, file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"imported": n})
}

func (s *Server) handleExportMaskRules(w http.ResponseWriter, r *http.Request) {
	_, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := s.perm.ExportMaskRulesCSV(r.URL.Query().Get("tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=mask_rules.csv")
	_, _ = w.Write(data)
}

func (s *Server) handleImportMaskRules(w http.ResponseWriter, r *http.Request) {
	tc, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "missing file field"})
		return
	}
	defer file.Close()
	n, err := s.perm.ImportMaskRulesCSV(tc, file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"imported": n})
}

func (s *Server) handleExportFieldPermissions(w http.ResponseWriter, r *http.Request) {
	_, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := s.perm.ExportFieldPermissionsCSV(r.URL.Query().Get("tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=field_permissions.csv")
	_, _ = w.Write(data)
}

func (s *Server) handleImportFieldPermissions(w http.ResponseWriter, r *http.Request) {
	tc, err := s.ctxFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": err.Error()})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "missing file field"})
		return
	}
	defer file.Close()
	n, err := s.perm.ImportFieldPermissionsCSV(tc, file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"imported": n})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
