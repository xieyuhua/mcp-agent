// Package admin 提供 MCP 数据服务器的权限可视化后台服务：
// REST API 用于配置角色策略与脱敏规则。仅 super_admin 可访问。
package admin

import (
	"fmt"
	"net/http"
	"strings"

	"company.com/mcp-data-server/internal/service"
	"company.com/mcp-data-server/internal/tenant"

	"github.com/gin-gonic/gin"
)

// Server 权限可视化后台服务。
type Server struct {
	auth *service.AuthService
	perm *service.PermissionService
}

// New 构造后台服务。
func New(authSvc *service.AuthService, permSvc *service.PermissionService) *Server {
	return &Server{auth: authSvc, perm: permSvc}
}

// Handler 返回后台 API 路由（http.Handler 兼容）。
func (s *Server) Handler() http.Handler {
	return s.buildRouter()
}

// buildRouter 配置 Gin 路由。
func (s *Server) buildRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api/admin")
	{
		api.POST("/login", s.handleLogin)
		api.GET("/roles", s.requireSuperAdmin(), s.handleRoles)
		api.POST("/roles", s.requireSuperAdmin(), s.handleRoles)
		api.DELETE("/roles", s.requireSuperAdmin(), s.handleRoleDelete)
		api.GET("/policies", s.requireSuperAdmin(), s.handlePolicies)
		api.POST("/policies", s.requireSuperAdmin(), s.handlePolicies)
		api.DELETE("/policies", s.requireSuperAdmin(), s.handlePolicyDelete)
		api.GET("/policies/export", s.requireSuperAdmin(), s.handleExportPolicies)
		api.POST("/policies/import", s.requireSuperAdmin(), s.handleImportPolicies)
		api.GET("/mask-rules", s.requireSuperAdmin(), s.handleMaskRules)
		api.POST("/mask-rules", s.requireSuperAdmin(), s.handleMaskRules)
		api.DELETE("/mask-rules", s.requireSuperAdmin(), s.handleMaskRuleDelete)
		api.GET("/mask-rules/export", s.requireSuperAdmin(), s.handleExportMaskRules)
		api.POST("/mask-rules/import", s.requireSuperAdmin(), s.handleImportMaskRules)
		api.GET("/field-permissions", s.requireSuperAdmin(), s.handleFieldPermissions)
		api.POST("/field-permissions", s.requireSuperAdmin(), s.handleFieldPermissions)
		api.DELETE("/field-permissions", s.requireSuperAdmin(), s.handleFieldPermissionDelete)
		api.GET("/field-permissions/export", s.requireSuperAdmin(), s.handleExportFieldPermissions)
		api.POST("/field-permissions/import", s.requireSuperAdmin(), s.handleImportFieldPermissions)
	}
	return r
}

// requireSuperAdmin 校验 Bearer/Cookie 令牌，并确认是 super_admin。
func (s *Server) requireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		tc, err := s.ctxFromRequest(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}
		c.Set("tenant", tc)
		c.Next()
	}
}

func (s *Server) ctxFromRequest(c *gin.Context) (*tenant.Context, error) {
	tok := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if tok == "" {
		if cookie, err := c.Cookie("token"); err == nil {
			tok = cookie
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

func tenantCtx(c *gin.Context) *tenant.Context {
	v, exists := c.Get("tenant")
	if !exists {
		return nil
	}
	return v.(*tenant.Context)
}

func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	tok, _, err := s.auth.Login(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": tok})
}

func (s *Server) handleRoles(c *gin.Context) {
	tc := tenantCtx(c)
	switch c.Request.Method {
	case http.MethodGet:
		roles, err := s.perm.ListRoles(tc, c.Query("tenant_id"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"roles": roles})
	case http.MethodPost:
		var req service.SetRoleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		v, err := s.perm.SetRole(tc, req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, v)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

func (s *Server) handleRoleDelete(c *gin.Context) {
	tc := tenantCtx(c)
	if err := s.perm.DeleteRole(tc, c.Query("tenant_id"), c.Query("name")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (s *Server) handlePolicies(c *gin.Context) {
	tc := tenantCtx(c)
	switch c.Request.Method {
	case http.MethodGet:
		views, err := s.perm.ListPolicies(tc, c.Query("tenant_id"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"policies": views})
	case http.MethodPost:
		var req service.SetPolicyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		v, err := s.perm.SetPolicy(tc, req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, v)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

func (s *Server) handlePolicyDelete(c *gin.Context) {
	tc := tenantCtx(c)
	if err := s.perm.DeletePolicy(tc, c.Query("tenant_id"), c.Query("role")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (s *Server) handleMaskRules(c *gin.Context) {
	tc := tenantCtx(c)
	switch c.Request.Method {
	case http.MethodGet:
		rules, err := s.perm.ListMaskRules(tc, c.Query("tenant_id"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"rules": rules})
	case http.MethodPost:
		var req service.SetMaskRuleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		v, err := s.perm.SetMaskRule(tc, req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, v)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

func (s *Server) handleMaskRuleDelete(c *gin.Context) {
	tc := tenantCtx(c)
	if err := s.perm.DeleteMaskRule(tc, c.Query("tenant_id"), c.Query("table"), c.Query("column")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (s *Server) handleFieldPermissions(c *gin.Context) {
	tc := tenantCtx(c)
	switch c.Request.Method {
	case http.MethodGet:
		views, err := s.perm.ListFieldPermissions(tc, c.Query("tenant_id"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"field_permissions": views})
	case http.MethodPost:
		var req service.SetFieldPermissionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		v, err := s.perm.SetFieldPermission(tc, req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, v)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

func (s *Server) handleFieldPermissionDelete(c *gin.Context) {
	tc := tenantCtx(c)
	if err := s.perm.DeleteFieldPermission(tc,
		c.Query("tenant_id"),
		c.Query("role"),
		c.Query("table"),
		c.Query("column")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (s *Server) handleExportPolicies(c *gin.Context) {
	data, err := s.perm.ExportPoliciesCSV(c.Query("tenant_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=policies.csv")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
}

func (s *Server) handleImportPolicies(c *gin.Context) {
	tc := tenantCtx(c)
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file field"})
		return
	}
	defer file.Close()
	n, err := s.perm.ImportPoliciesCSV(tc, file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"imported": n})
}

func (s *Server) handleExportMaskRules(c *gin.Context) {
	data, err := s.perm.ExportMaskRulesCSV(c.Query("tenant_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=mask_rules.csv")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
}

func (s *Server) handleImportMaskRules(c *gin.Context) {
	tc := tenantCtx(c)
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file field"})
		return
	}
	defer file.Close()
	n, err := s.perm.ImportMaskRulesCSV(tc, file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"imported": n})
}

func (s *Server) handleExportFieldPermissions(c *gin.Context) {
	data, err := s.perm.ExportFieldPermissionsCSV(c.Query("tenant_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=field_permissions.csv")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
}

func (s *Server) handleImportFieldPermissions(c *gin.Context) {
	tc := tenantCtx(c)
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file field"})
		return
	}
	defer file.Close()
	n, err := s.perm.ImportFieldPermissionsCSV(tc, file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"imported": n})
}
