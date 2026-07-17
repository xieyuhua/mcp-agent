package service

import (
	"fmt"
	"time"

	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/mask"
	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/tenant"
)

// PermissionService 权限可视化配置服务：查看/设置角色策略与脱敏规则。
// 仅平台运营（super_admin）可修改；修改后立即刷新内存缓存并生效。
type PermissionService struct {
	permRepo *repository.PermissionRepo
	authz    *auth.Resolver
	masker   *mask.Resolver
	audit    *AuditService
}

func NewPermissionService(permRepo *repository.PermissionRepo, authz *auth.Resolver, masker *mask.Resolver, audit *AuditService) *PermissionService {
	return &PermissionService{permRepo: permRepo, authz: authz, masker: masker, audit: audit}
}

// --- 角色策略可视化 ---

// ListPolicies 列出某租户（含平台默认）的全部角色策略，返回可视化结构。
func (s *PermissionService) ListPolicies(t *tenant.Context, tenantID string) ([]PolicyView, error) {
	if err := s.requireAdmin(t); err != nil {
		return nil, err
	}
	if tenantID == "" {
		tenantID = t.TenantID
	}
	policies, err := s.permRepo.ListPolicies(tenantID)
	if err != nil {
		return nil, err
	}
	views := make([]PolicyView, 0, len(policies))
	for _, p := range policies {
		scope := p.DataScope
		if scope == "" {
			scope = scopeName(auth.DefaultScope(p.Role))
		}
		tables := auth.ParseAllowedTables(p.AllowedTables)
		if len(tables) == 0 {
			for k := range auth.DefaultAllowedTables(p.Role) {
				tables = append(tables, k)
			}
		}
		views = append(views, PolicyView{
			TenantID:     p.TenantID,
			Role:         p.Role,
			IsGlobal:     p.TenantID == "",
			DataScope:    scope,
			AllowedTables: tables,
			CanRawSQL:    p.CanRawSQL,
			UpdatedBy:    p.UpdatedBy,
			UpdatedAt:    p.UpdatedAt.Format(time.RFC3339),
		})
	}
	s.writeAudit(t, "perm_list", "permission_policies", fmt.Sprintf("tenant=%s", tenantID), len(views))
	return views, nil
}

// SetPolicy 新增或更新某租户（或平台全局）的角色策略。
func (s *PermissionService) SetPolicy(t *tenant.Context, req SetPolicyRequest) (*PolicyView, error) {
	if err := s.requireAdmin(t); err != nil {
		return nil, err
	}
	if !validScope(req.DataScope) {
		return nil, fmt.Errorf("invalid data_scope %q, want one of all|tenant|region|store", req.DataScope)
	}
	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = t.TenantID // 默认配置到当前运营所在租户
	}
	p := &model.PermissionPolicy{
		TenantID:     tenantID,
		Role:         req.Role,
		DataScope:    req.DataScope,
		AllowedTables: joinTables(req.AllowedTables),
		CanRawSQL:    req.CanRawSQL,
		UpdatedBy:    t.UserID,
		UpdatedAt:    time.Now(),
	}
	if err := s.permRepo.UpsertPolicy(p); err != nil {
		return nil, err
	}
	// 刷新缓存，立即生效
	_ = s.authz.Refresh(t.TenantID)
	s.writeAudit(t, "perm_set", "permission_policies", fmt.Sprintf("%s/%s", tenantID, req.Role), 1)
	return s.toPolicyView(p), nil
}

// DeletePolicy 删除某租户级角色策略（回退到平台默认）。
func (s *PermissionService) DeletePolicy(t *tenant.Context, tenantID, role string) error {
	if err := s.requireAdmin(t); err != nil {
		return err
	}
	if err := s.permRepo.DeletePolicy(tenantID, role); err != nil {
		return err
	}
	_ = s.authz.Refresh(t.TenantID)
	s.writeAudit(t, "perm_delete", "permission_policies", fmt.Sprintf("%s/%s", tenantID, role), 0)
	return nil
}

// --- 脱敏规则可视化 ---

// ListMaskRules 列出某租户（含平台默认）的全部脱敏规则。
func (s *PermissionService) ListMaskRules(t *tenant.Context, tenantID string) ([]MaskRuleView, error) {
	if err := s.requireAdmin(t); err != nil {
		return nil, err
	}
	if tenantID == "" {
		tenantID = t.TenantID
	}
	rules, err := s.permRepo.ListMaskRules(tenantID)
	if err != nil {
		return nil, err
	}
	views := make([]MaskRuleView, 0, len(rules))
	for _, r := range rules {
		views = append(views, MaskRuleView{
			TenantID: r.TenantID,
			Table:    r.TableName,
			Column:   r.Column,
			MaskType: r.MaskType,
			Enabled:  r.Enabled,
			IsGlobal: r.TenantID == "",
			UpdatedBy: r.UpdatedBy,
			UpdatedAt: r.UpdatedAt.Format(time.RFC3339),
		})
	}
	s.writeAudit(t, "mask_list", "mask_rules", fmt.Sprintf("tenant=%s", tenantID), len(views))
	return views, nil
}

// SetMaskRule 新增或更新某租户（或平台全局）的脱敏规则。
func (s *PermissionService) SetMaskRule(t *tenant.Context, req SetMaskRuleRequest) (*MaskRuleView, error) {
	if err := s.requireAdmin(t); err != nil {
		return nil, err
	}
	if !validMaskType(req.MaskType) {
		return nil, fmt.Errorf("invalid mask_type %q, want one of phone|email|idcard|name|money|secret", req.MaskType)
	}
	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = t.TenantID
	}
	r := &model.MaskRule{
		TenantID:  tenantID,
		TableName: req.Table,
		Column:    req.Column,
		MaskType:  req.MaskType,
		Enabled:   req.Enabled,
		UpdatedBy: t.UserID,
		UpdatedAt: time.Now(),
	}
	if err := s.permRepo.UpsertMaskRule(r); err != nil {
		return nil, err
	}
	_ = s.masker.Refresh(t.TenantID)
	s.writeAudit(t, "mask_set", "mask_rules", fmt.Sprintf("%s/%s.%s", tenantID, req.Table, req.Column), 1)
	return &MaskRuleView{
		TenantID: tenantID, Table: r.TableName, Column: r.Column,
		MaskType: r.MaskType, Enabled: r.Enabled, IsGlobal: tenantID == "",
		UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// DeleteMaskRule 删除某租户级脱敏规则。
func (s *PermissionService) DeleteMaskRule(t *tenant.Context, tenantID, table, column string) error {
	if err := s.requireAdmin(t); err != nil {
		return err
	}
	if err := s.permRepo.DeleteMaskRule(tenantID, table, column); err != nil {
		return err
	}
	_ = s.masker.Refresh(t.TenantID)
	s.writeAudit(t, "mask_delete", "mask_rules", fmt.Sprintf("%s/%s.%s", tenantID, table, column), 0)
	return nil
}

// --- 视图与请求结构 ---

type PolicyView struct {
	TenantID      string   `json:"tenant_id"`
	Role          string   `json:"role"`
	IsGlobal      bool     `json:"is_global"`
	DataScope     string   `json:"data_scope"`
	AllowedTables []string `json:"allowed_tables"`
	CanRawSQL     bool     `json:"can_raw_sql"`
	UpdatedBy     string   `json:"updated_by"`
	UpdatedAt     string   `json:"updated_at"`
}

type SetPolicyRequest struct {
	TenantID     string   `json:"tenant_id"` // 空=当前租户
	Role         string   `json:"role"`
	DataScope    string   `json:"data_scope"` // all|tenant|region|store
	AllowedTables []string `json:"allowed_tables"`
	CanRawSQL    bool     `json:"can_raw_sql"`
}

type MaskRuleView struct {
	TenantID  string `json:"tenant_id"`
	Table     string `json:"table"`
	Column    string `json:"column"`
	MaskType  string `json:"mask_type"`
	Enabled   bool   `json:"enabled"`
	IsGlobal  bool   `json:"is_global"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

type SetMaskRuleRequest struct {
	TenantID string `json:"tenant_id"`
	Table    string `json:"table"`
	Column   string `json:"column"`
	MaskType string `json:"mask_type"`
	Enabled  bool   `json:"enabled"`
}

// --- 辅助 ---

func (s *PermissionService) requireAdmin(t *tenant.Context) error {
	if t.Role != auth.RoleSuperAdmin {
		return fmt.Errorf("only super_admin can manage permission settings")
	}
	return nil
}

func (s *PermissionService) toPolicyView(p *model.PermissionPolicy) *PolicyView {
	tables := auth.ParseAllowedTables(p.AllowedTables)
	if len(tables) == 0 {
		for k := range auth.DefaultAllowedTables(p.Role) {
			tables = append(tables, k)
		}
	}
	return &PolicyView{
		TenantID: p.TenantID, Role: p.Role, IsGlobal: p.TenantID == "",
		DataScope: p.DataScope, AllowedTables: tables, CanRawSQL: p.CanRawSQL,
		UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
	}
}

func (s *PermissionService) writeAudit(t *tenant.Context, action, table, query string, rows int) {
	_ = s.audit.Record(&model.AuditLog{
		TenantID: t.TenantID, UserID: t.UserID, Action: action, Tool: action,
		TableName: table, Query: query, RowCount: rows, IP: "mcp", CreatedAt: time.Now(),
	})
}

func scopeName(s auth.DataScope) string {
	switch s {
	case auth.ScopeAll:
		return "all"
	case auth.ScopeTenant:
		return "tenant"
	case auth.ScopeRegion:
		return "region"
	default:
		return "store"
	}
}

func validScope(s string) bool {
	for _, v := range model.ValidScopes {
		if v == s {
			return true
		}
	}
	return false
}

func validMaskType(s string) bool {
	for _, v := range model.ValidMaskTypes {
		if v == s {
			return true
		}
	}
	return false
}

func joinTables(tables []string) string {
	out := ""
	for i, t := range tables {
		if i > 0 {
			out += ","
		}
		out += t
	}
	return out
}
