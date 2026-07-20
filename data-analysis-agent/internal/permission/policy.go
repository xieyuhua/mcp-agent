package permission

import (
	"strings"
	"sync"
)

// 角色定义。
const (
	RoleSuperAdmin = "super_admin"
	RoleRegionMgr  = "region_manager"
	RoleStoreMgr   = "store_manager"
	RoleStaff      = "staff"
	RoleAnalyst    = "analyst"
)

// DataScope 数据可见范围。
type DataScope int

const (
	ScopeAll DataScope = iota
	ScopeTenant
	ScopeRegion
	ScopeStore
)

func scopeFromString(s string) DataScope {
	switch s {
	case "all":
		return ScopeAll
	case "tenant":
		return ScopeTenant
	case "region":
		return ScopeRegion
	default:
		return ScopeStore
	}
}

// DefaultScope 角色默认数据范围。
func DefaultScope(role string) DataScope {
	switch role {
	case RoleSuperAdmin:
		return ScopeAll
	case RoleRegionMgr:
		return ScopeRegion
	case RoleStoreMgr, RoleStaff:
		return ScopeStore
	case RoleAnalyst:
		return ScopeTenant
	default:
		return ScopeStore
	}
}

// DefaultAllowedTables 角色默认表白名单。
func DefaultAllowedTables(role string) map[string]bool {
	switch role {
	case RoleSuperAdmin:
		return map[string]bool{"customers": true, "orders": true, "users": true, "tenants": true, "audit_logs": true}
	case RoleAnalyst, RoleRegionMgr, RoleStoreMgr, RoleStaff:
		return map[string]bool{"customers": true, "orders": true}
	default:
		return map[string]bool{}
	}
}

// DefaultCanRawSQL 角色默认是否允许原生 SQL。
func DefaultCanRawSQL(role string) bool {
	return role == RoleSuperAdmin
}

// PolicyStore 权限策略存储接口。
type PolicyStore interface {
	ListPolicies(tenantID string) ([]PermissionPolicy, error)
	ListFieldPermissions(tenantID string) ([]FieldPermission, error)
}

// Resolver 从数据库读取权限策略，带内存缓存。
type Resolver struct {
	store PolicyStore
	mu    sync.RWMutex
	cache map[string]*PermissionPolicy
	field map[string]map[string]map[string]bool
}

func NewResolver(store PolicyStore) *Resolver {
	return &Resolver{store: store, cache: map[string]*PermissionPolicy{}, field: map[string]map[string]map[string]bool{}}
}

func cacheKey(tenantID, role string) string { return tenantID + "\x00" + role }

// Refresh 从数据库重新加载某租户的策略到缓存。
func (r *Resolver) Refresh(tenantID string) error {
	policies, err := r.store.ListPolicies(tenantID)
	if err != nil {
		return err
	}
	fields, err := r.store.ListFieldPermissions(tenantID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range r.cache {
		if v.TenantID == tenantID || v.TenantID == "" {
			delete(r.cache, k)
		}
	}
	for k := range r.field {
		parts := strings.SplitN(k, "\x00", 2)
		if len(parts) == 2 {
			if t := strings.TrimSpace(parts[0]); t == tenantID || t == "" {
				delete(r.field, k)
			}
		}
	}
	for i := range policies {
		p := policies[i]
		r.cache[cacheKey(p.TenantID, p.Role)] = &p
	}
	for i := range fields {
		fp := fields[i]
		key := cacheKey(fp.TenantID, fp.Role)
		if r.field[key] == nil {
			r.field[key] = map[string]map[string]bool{}
		}
		if r.field[key][fp.TableName] == nil {
			r.field[key][fp.TableName] = map[string]bool{}
		}
		r.field[key][fp.TableName][fp.Column] = fp.Hidden
	}
	return nil
}

// Scope 返回角色的数据范围。
func (r *Resolver) Scope(tenantID, role string) DataScope {
	if p := r.lookup(tenantID, role); p != nil {
		return scopeFromString(p.DataScope)
	}
	return DefaultScope(role)
}

// AllowedTables 返回角色可访问的表。
func (r *Resolver) AllowedTables(tenantID, role string) map[string]bool {
	if p := r.lookup(tenantID, role); p != nil {
		out := map[string]bool{}
		for _, t := range ParseAllowedTables(p.AllowedTables) {
			out[t] = true
		}
		return out
	}
	return DefaultAllowedTables(role)
}

// CanRunRawSQL 是否允许执行原生 SQL。
func (r *Resolver) CanRunRawSQL(tenantID, role string) bool {
	if p := r.lookup(tenantID, role); p != nil {
		return p.CanRawSQL
	}
	return DefaultCanRawSQL(role)
}

// HiddenFields 返回角色隐藏字段。
func (r *Resolver) HiddenFields(tenantID, role string) map[string]map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if m, ok := r.field[cacheKey(tenantID, role)]; ok {
		return m
	}
	if m, ok := r.field[cacheKey("", role)]; ok {
		return m
	}
	return map[string]map[string]bool{}
}

func (r *Resolver) lookup(tenantID, role string) *PermissionPolicy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.cache[cacheKey(tenantID, role)]; ok {
		return p
	}
	if p, ok := r.cache[cacheKey("", role)]; ok {
		return p
	}
	return nil
}

// ScopeFilter 根据数据范围生成 WHERE 条件（列名 → 值）。
// userIDs 包含用户的 tenant_id / region_id / store_id 等上下文标识。
// 返回 nil 表示无需过滤（全部可见）。
func (r *Resolver) ScopeFilter(tenantID, role string, userIDs map[string]string) map[string]string {
	scope := r.Scope(tenantID, role)
	switch scope {
	case ScopeAll:
		return nil
	case ScopeTenant:
		if id := userIDs["tenant_id"]; id != "" {
			return map[string]string{"tenant_id": id}
		}
	case ScopeRegion:
		if id := userIDs["region_id"]; id != "" {
			return map[string]string{"region_id": id}
		}
	case ScopeStore:
		if id := userIDs["store_id"]; id != "" {
			return map[string]string{"store_id": id}
		}
	}
	// 有范围但缺少用户上下文 ID，返回 nil 则不做行级过滤（但权限表校验仍生效）
	return nil
}

// ScopeFilterExpr 返回适用于 SQL WHERE 子句的表达式字符串（如 "tenant_id = 't001'"）。
// 用于 run_sql 等原生 SQL 场景。返回值不包含 WHERE 关键字。
func (r *Resolver) ScopeFilterExpr(tenantID, role string, userIDs map[string]string) string {
	f := r.ScopeFilter(tenantID, role, userIDs)
	if len(f) == 0 {
		return ""
	}
	for col, val := range f {
		return col + " = '" + strings.ReplaceAll(val, "'", "''") + "'"
	}
	return ""
}

// ParseAllowedTables 解析逗号分隔的表白名单。
func ParseAllowedTables(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
