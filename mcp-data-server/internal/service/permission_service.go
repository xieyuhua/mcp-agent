package service

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/mask"
	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/tenant"
)

// PermissionService 权限可视化配置服务：查看/设置角色策略、脱敏规则、字段权限与角色定义。
// 仅平台运营（super_admin）可修改；修改后立即刷新内存缓存并生效。
type PermissionService struct {
	permRepo *repository.PermissionRepo
	roleRepo *repository.RoleRepo
	authz    *auth.Resolver
	masker   *mask.Resolver
	audit    *AuditService
}

func NewPermissionService(permRepo *repository.PermissionRepo, roleRepo *repository.RoleRepo, authz *auth.Resolver, masker *mask.Resolver, audit *AuditService) *PermissionService {
	return &PermissionService{permRepo: permRepo, roleRepo: roleRepo, authz: authz, masker: masker, audit: audit}
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
			TenantID:      p.TenantID,
			Role:          p.Role,
			IsGlobal:      p.TenantID == "",
			DataScope:     scope,
			AllowedTables: tables,
			CanRawSQL:     p.CanRawSQL,
			UpdatedBy:     p.UpdatedBy,
			UpdatedAt:     p.UpdatedAt.Format(time.RFC3339),
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
		TenantID:      tenantID,
		Role:          req.Role,
		DataScope:     req.DataScope,
		AllowedTables: joinTables(req.AllowedTables),
		CanRawSQL:     req.CanRawSQL,
		UpdatedBy:     t.UserID,
		UpdatedAt:     time.Now(),
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
			TenantID:  r.TenantID,
			Table:     r.TableName,
			Column:    r.Column,
			MaskType:  r.MaskType,
			Enabled:   r.Enabled,
			IsGlobal:  r.TenantID == "",
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

// --- 字段权限可视化 ---

// ListFieldPermissions 列出某租户（含平台默认）的全部字段权限。
func (s *PermissionService) ListFieldPermissions(t *tenant.Context, tenantID string) ([]FieldPermissionView, error) {
	if err := s.requireAdmin(t); err != nil {
		return nil, err
	}
	if tenantID == "" {
		tenantID = t.TenantID
	}
	rules, err := s.permRepo.ListFieldPermissions(tenantID)
	if err != nil {
		return nil, err
	}
	views := make([]FieldPermissionView, 0, len(rules))
	for _, r := range rules {
		views = append(views, FieldPermissionView{
			TenantID:  r.TenantID,
			Role:      r.Role,
			Table:     r.TableName,
			Column:    r.Column,
			Hidden:    r.Hidden,
			IsGlobal:  r.TenantID == "",
			UpdatedBy: r.UpdatedBy,
			UpdatedAt: r.UpdatedAt.Format(time.RFC3339),
		})
	}
	s.writeAudit(t, "field_perm_list", "field_permissions", fmt.Sprintf("tenant=%s", tenantID), len(views))
	return views, nil
}

// SetFieldPermission 新增或更新某租户（或平台全局）的字段可见性。
func (s *PermissionService) SetFieldPermission(t *tenant.Context, req SetFieldPermissionRequest) (*FieldPermissionView, error) {
	if err := s.requireAdmin(t); err != nil {
		return nil, err
	}
	if !validIdentifier(req.Table) || !validIdentifier(req.Column) {
		return nil, fmt.Errorf("table and column must be valid identifiers")
	}
	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = t.TenantID
	}
	fp := &model.FieldPermission{
		TenantID:  tenantID,
		Role:      req.Role,
		TableName: req.Table,
		Column:    req.Column,
		Hidden:    req.Hidden,
		UpdatedBy: t.UserID,
		UpdatedAt: time.Now(),
	}
	if err := s.permRepo.UpsertFieldPermission(fp); err != nil {
		return nil, err
	}
	_ = s.authz.Refresh(t.TenantID)
	s.writeAudit(t, "field_perm_set", "field_permissions", fmt.Sprintf("%s/%s.%s=%v", tenantID, req.Table, req.Column, req.Hidden), 1)
	return &FieldPermissionView{
		TenantID: tenantID, Role: fp.Role, Table: fp.TableName, Column: fp.Column,
		Hidden: fp.Hidden, IsGlobal: tenantID == "",
		UpdatedBy: fp.UpdatedBy, UpdatedAt: fp.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// DeleteFieldPermission 删除某租户级字段权限。
func (s *PermissionService) DeleteFieldPermission(t *tenant.Context, tenantID, role, table, column string) error {
	if err := s.requireAdmin(t); err != nil {
		return err
	}
	if err := s.permRepo.DeleteFieldPermission(tenantID, role, table, column); err != nil {
		return err
	}
	_ = s.authz.Refresh(t.TenantID)
	s.writeAudit(t, "field_perm_delete", "field_permissions", fmt.Sprintf("%s/%s.%s", tenantID, table, column), 0)
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
	TenantID      string   `json:"tenant_id"` // 空=当前租户
	Role          string   `json:"role"`
	DataScope     string   `json:"data_scope"` // all|tenant|region|store
	AllowedTables []string `json:"allowed_tables"`
	CanRawSQL     bool     `json:"can_raw_sql"`
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

type FieldPermissionView struct {
	TenantID  string `json:"tenant_id"`
	Role      string `json:"role"`
	Table     string `json:"table"`
	Column    string `json:"column"`
	Hidden    bool   `json:"hidden"`
	IsGlobal  bool   `json:"is_global"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

type SetFieldPermissionRequest struct {
	TenantID string `json:"tenant_id"` // 空=当前租户
	Role     string `json:"role"`
	Table    string `json:"table"`
	Column   string `json:"column"`
	Hidden   bool   `json:"hidden"` // true=隐藏，false=显式可见
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

func validIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, ch := range s {
		if i == 0 {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_') {
				return false
			}
			continue
		}
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}

// --- 批量导入/导出（CSV） ---

// ExportPoliciesCSV 导出策略为 CSV 字节。
func (s *PermissionService) ExportPoliciesCSV(tenantID string) ([]byte, error) {
	policies, err := s.permRepo.ListPolicies(tenantID)
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	_ = w.Write([]string{"tenant_id", "role", "data_scope", "allowed_tables", "can_raw_sql", "updated_by", "updated_at"})
	for _, p := range policies {
		_ = w.Write([]string{
			p.TenantID, p.Role, p.DataScope, p.AllowedTables,
			strconv.FormatBool(p.CanRawSQL), p.UpdatedBy, p.UpdatedAt.Format(time.RFC3339),
		})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// ImportPoliciesCSV 从 CSV 导入策略，返回导入条数。
func (s *PermissionService) ImportPoliciesCSV(t *tenant.Context, r io.Reader) (int, error) {
	if err := s.requireAdmin(t); err != nil {
		return 0, err
	}
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}
	// 找列索引
	idx := csvHeaderIndex(records[0])
	if _, ok := idx["role"]; !ok {
		return 0, fmt.Errorf("CSV must contain a 'role' column")
	}
	now := time.Now()
	imported := 0
	for i, row := range records[1:] {
		if len(row) == 0 {
			continue
		}
		role := csvVal(row, idx, "role")
		if role == "" {
			return 0, fmt.Errorf("row %d: role is empty", i+2)
		}
		scope := csvVal(row, idx, "data_scope")
		if scope == "" {
			scope = "store"
		}
		if !validScope(scope) {
			return 0, fmt.Errorf("row %d: invalid data_scope %q", i+2, scope)
		}
		tables := csvVal(row, idx, "allowed_tables")
		canRaw := false
		if v := csvVal(row, idx, "can_raw_sql"); v != "" {
			canRaw, _ = strconv.ParseBool(v)
		}
		tenantID := csvVal(row, idx, "tenant_id")
		if tenantID == "" {
			tenantID = t.TenantID
		}
		p := &model.PermissionPolicy{
			TenantID:      tenantID,
			Role:          role,
			DataScope:     scope,
			AllowedTables: tables,
			CanRawSQL:     canRaw,
			UpdatedBy:     t.UserID,
			UpdatedAt:     now,
		}
		if err := s.permRepo.UpsertPolicy(p); err != nil {
			return imported, fmt.Errorf("row %d: %w", i+2, err)
		}
		imported++
	}
	_ = s.authz.Refresh(t.TenantID)
	s.writeAudit(t, "perm_import", "permission_policies", "", imported)
	return imported, nil
}

// ExportFieldPermissionsCSV 导出字段权限为 CSV 字节。
func (s *PermissionService) ExportFieldPermissionsCSV(tenantID string) ([]byte, error) {
	rules, err := s.permRepo.ListFieldPermissions(tenantID)
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	_ = w.Write([]string{"tenant_id", "role", "table", "column", "hidden", "updated_by", "updated_at"})
	for _, r := range rules {
		_ = w.Write([]string{
			r.TenantID, r.Role, r.TableName, r.Column,
			strconv.FormatBool(r.Hidden), r.UpdatedBy, r.UpdatedAt.Format(time.RFC3339),
		})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// ImportFieldPermissionsCSV 从 CSV 导入字段权限，返回导入条数。
func (s *PermissionService) ImportFieldPermissionsCSV(t *tenant.Context, r io.Reader) (int, error) {
	if err := s.requireAdmin(t); err != nil {
		return 0, err
	}
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}
	idx := csvHeaderIndex(records[0])
	need := []string{"role", "table", "column"}
	for _, k := range need {
		if _, ok := idx[k]; !ok {
			return 0, fmt.Errorf("CSV must contain a '%s' column", k)
		}
	}
	now := time.Now()
	imported := 0
	for i, row := range records[1:] {
		role := csvVal(row, idx, "role")
		table := csvVal(row, idx, "table")
		column := csvVal(row, idx, "column")
		if role == "" || table == "" || column == "" {
			return 0, fmt.Errorf("row %d: role/table/column required", i+2)
		}
		hidden := true
		if v := csvVal(row, idx, "hidden"); v != "" {
			hidden, _ = strconv.ParseBool(v)
		}
		tenantID := csvVal(row, idx, "tenant_id")
		if tenantID == "" {
			tenantID = t.TenantID
		}
		fp := &model.FieldPermission{
			TenantID:  tenantID,
			Role:      role,
			TableName: table,
			Column:    column,
			Hidden:    hidden,
			UpdatedBy: t.UserID,
			UpdatedAt: now,
		}
		if err := s.permRepo.UpsertFieldPermission(fp); err != nil {
			return imported, fmt.Errorf("row %d: %w", i+2, err)
		}
		imported++
	}
	_ = s.authz.Refresh(t.TenantID)
	s.writeAudit(t, "field_perm_import", "field_permissions", "", imported)
	return imported, nil
}

// ExportMaskRulesCSV 导出脱敏规则为 CSV 字节。
func (s *PermissionService) ExportMaskRulesCSV(tenantID string) ([]byte, error) {
	rules, err := s.permRepo.ListMaskRules(tenantID)
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	_ = w.Write([]string{"tenant_id", "table", "column", "mask_type", "enabled", "updated_by", "updated_at"})
	for _, r := range rules {
		_ = w.Write([]string{
			r.TenantID, r.TableName, r.Column, r.MaskType,
			strconv.FormatBool(r.Enabled), r.UpdatedBy, r.UpdatedAt.Format(time.RFC3339),
		})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// ImportMaskRulesCSV 从 CSV 导入脱敏规则，返回导入条数。
func (s *PermissionService) ImportMaskRulesCSV(t *tenant.Context, r io.Reader) (int, error) {
	if err := s.requireAdmin(t); err != nil {
		return 0, err
	}
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}
	idx := csvHeaderIndex(records[0])
	need := []string{"table", "column", "mask_type"}
	for _, k := range need {
		if _, ok := idx[k]; !ok {
			return 0, fmt.Errorf("CSV must contain a '%s' column", k)
		}
	}
	now := time.Now()
	imported := 0
	for i, row := range records[1:] {
		table := csvVal(row, idx, "table")
		column := csvVal(row, idx, "column")
		maskType := csvVal(row, idx, "mask_type")
		if table == "" || column == "" || maskType == "" {
			return 0, fmt.Errorf("row %d: table/column/mask_type required", i+2)
		}
		if !validMaskType(maskType) {
			return 0, fmt.Errorf("row %d: invalid mask_type %q", i+2, maskType)
		}
		enabled := true
		if v := csvVal(row, idx, "enabled"); v != "" {
			enabled, _ = strconv.ParseBool(v)
		}
		tenantID := csvVal(row, idx, "tenant_id")
		if tenantID == "" {
			tenantID = t.TenantID
		}
		mr := &model.MaskRule{
			TenantID:  tenantID,
			TableName: table,
			Column:    column,
			MaskType:  maskType,
			Enabled:   enabled,
			UpdatedBy: t.UserID,
			UpdatedAt: now,
		}
		if err := s.permRepo.UpsertMaskRule(mr); err != nil {
			return imported, fmt.Errorf("row %d: %w", i+2, err)
		}
		imported++
	}
	_ = s.masker.Refresh(t.TenantID)
	s.writeAudit(t, "mask_import", "mask_rules", "", imported)
	return imported, nil
}

// csvHeaderIndex 把 CSV 表头映射为列名->索引。
func csvHeaderIndex(header []string) map[string]int {
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(strings.ToLower(h))] = i
	}
	return idx
}

// csvVal 按列名取单元格值，不存在则返回空串。
func csvVal(row []string, idx map[string]int, key string) string {
	if i, ok := idx[key]; ok && i < len(row) {
		return strings.TrimSpace(row[i])
	}
	return ""
}

// --- 角色管理 ---

// RoleView 角色视图。
type RoleView struct {
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	IsBuiltin   bool   `json:"is_builtin"`
	UpdatedBy   string `json:"updated_by"`
	UpdatedAt   string `json:"updated_at"`
}

// SetRoleRequest 新增/更新角色请求。
type SetRoleRequest struct {
	TenantID    string `json:"tenant_id"` // 空=平台全局
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

// ListRoles 列出角色（含平台全局）。
func (s *PermissionService) ListRoles(t *tenant.Context, tenantID string) ([]RoleView, error) {
	if err := s.requireAdmin(t); err != nil {
		return nil, err
	}
	if tenantID == "" {
		tenantID = t.TenantID
	}
	roles, err := s.roleRepo.ListRoles(tenantID)
	if err != nil {
		return nil, err
	}
	views := make([]RoleView, 0, len(roles))
	for _, r := range roles {
		views = append(views, RoleView{
			TenantID:    r.TenantID,
			Name:        r.Name,
			DisplayName: r.DisplayName,
			Description: r.Description,
			IsBuiltin:   r.IsBuiltin,
			UpdatedBy:   r.UpdatedBy,
			UpdatedAt:   r.UpdatedAt.Format(time.RFC3339),
		})
	}
	return views, nil
}

// SetRole 新增或更新角色（内置角色仅可改描述）。
func (s *PermissionService) SetRole(t *tenant.Context, req SetRoleRequest) (*RoleView, error) {
	if err := s.requireAdmin(t); err != nil {
		return nil, err
	}
	if !validIdentifier(req.Name) {
		return nil, fmt.Errorf("role name must be a valid identifier")
	}
	if req.DisplayName == "" {
		return nil, fmt.Errorf("display_name is required")
	}
	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = t.TenantID
	}
	role := &model.Role{
		TenantID:    tenantID,
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		UpdatedBy:   t.UserID,
		UpdatedAt:   time.Now(),
	}
	if err := s.roleRepo.UpsertRole(role); err != nil {
		return nil, err
	}
	s.writeAudit(t, "role_set", "roles", fmt.Sprintf("%s/%s", tenantID, req.Name), 1)
	return &RoleView{
		TenantID:    role.TenantID,
		Name:        role.Name,
		DisplayName: role.DisplayName,
		Description: role.Description,
		IsBuiltin:   role.IsBuiltin,
		UpdatedBy:   role.UpdatedBy,
		UpdatedAt:   role.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// DeleteRole 删除非内置角色。
func (s *PermissionService) DeleteRole(t *tenant.Context, tenantID, name string) error {
	if err := s.requireAdmin(t); err != nil {
		return err
	}
	if tenantID == "" {
		tenantID = t.TenantID
	}
	if err := s.roleRepo.DeleteRole(tenantID, name); err != nil {
		return err
	}
	s.writeAudit(t, "role_delete", "roles", fmt.Sprintf("%s/%s", tenantID, name), 0)
	return nil
}
