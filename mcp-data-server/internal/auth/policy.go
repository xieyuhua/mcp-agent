package auth

import (
	"strings"
	"sync"

	"company.com/mcp-data-server/internal/model"
)

// PolicyStore 权限策略的存储接口，由 repository.PermissionRepo 实现。
// 以接口形式依赖，避免 auth 与 repository 形成循环依赖。
type PolicyStore interface {
	ListPolicies(tenantID string) ([]model.PermissionPolicy, error)
}

// 角色定义（多租户 SaaS 权限模型）。
const (
	RoleSuperAdmin = "super_admin"  // 平台/后台运营：跨租户、全部数据
	RoleRegionMgr  = "region_manager" // 区域负责人：本租户 + 本区域
	RoleStoreMgr   = "store_manager"  // 门店店长：本租户 + 本门店
	RoleStaff      = "staff"          // 普通岗位：本租户 + 本门店
	RoleAnalyst    = "analyst"        // 数据分析师：本租户只读
)

// DataScope 数据可见范围，决定行级隔离策略。
type DataScope int

const (
	ScopeAll DataScope = iota // 全部租户
	ScopeTenant               // 本租户
	ScopeRegion               // 本租户 + 本区域
	ScopeStore                // 本租户 + 本门店
)

// scopeFromString 将配置字符串解析为 DataScope。
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

// DefaultScope 角色默认数据范围（数据库无配置时回退）。
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

// DefaultAllowedTables 角色默认表白名单（数据库无配置时回退）。
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

// Resolver 从数据库读取权限策略，带内存缓存（TTL 由调用方 Refresh 控制）。
// 这样权限配置可在运行时可视化修改并立即生效，无需重启。
type Resolver struct {
	store PolicyStore
	mu    sync.RWMutex
	cache map[string]*model.PermissionPolicy // key: tenantID+"\x00"+role
}

func NewResolver(store PolicyStore) *Resolver {
	return &Resolver{store: store, cache: map[string]*model.PermissionPolicy{}}
}

func cacheKey(tenantID, role string) string { return tenantID + "\x00" + role }

// Refresh 从数据库重新加载某租户（含平台默认）的策略到缓存。
func (r *Resolver) Refresh(tenantID string) error {
	policies, err := r.store.ListPolicies(tenantID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// 仅刷新与该租户相关（含平台默认）的条目
	for k, v := range r.cache {
		if v.TenantID == tenantID || v.TenantID == "" {
			delete(r.cache, k)
		}
	}
	for i := range policies {
		p := policies[i]
		r.cache[cacheKey(p.TenantID, p.Role)] = &p
	}
	return nil
}

// Scope 返回角色的数据范围（缓存/默认回退）。
func (r *Resolver) Scope(tenantID, role string) DataScope {
	if p := r.lookup(tenantID, role); p != nil {
		return scopeFromString(p.DataScope)
	}
	return DefaultScope(role)
}

// AllowedTables 返回角色可访问的数据表白名单（缓存/默认回退）。
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

// CanRunRawSQL 是否允许执行原生 SQL（缓存/默认回退）。
func (r *Resolver) CanRunRawSQL(tenantID, role string) bool {
	if p := r.lookup(tenantID, role); p != nil {
		return p.CanRawSQL
	}
	return DefaultCanRawSQL(role)
}

// lookup 优先租户级，再平台默认。
func (r *Resolver) lookup(tenantID, role string) *model.PermissionPolicy {
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
