package mask

import (
	"strings"
	"sync"

	"company.com/mcp-data-server/internal/model"
)

// MaskStore 脱敏规则的存储接口，由 repository.PermissionRepo 实现。
type MaskStore interface {
	GetMaskRulesAsMap(tenantID string) (map[string]map[string]model.MaskRule, error)
}

// MaskType 脱敏类型。
type MaskType string

const (
	MaskPhone  MaskType = "phone"
	MaskEmail  MaskType = "email"
	MaskIDCard MaskType = "idcard"
	MaskName   MaskType = "name"
	MaskMoney  MaskType = "money"
	MaskSecret MaskType = "secret"
)

// Rules 脱敏规则：表 -> 列 -> 脱敏类型。
type Rules map[string]map[string]MaskType

// MaskValue 对单个值按类型脱敏。
func MaskValue(t MaskType, v string) string {
	switch t {
	case MaskPhone:
		if len(v) >= 7 {
			return v[:3] + "****" + v[len(v)-4:]
		}
		return "****"
	case MaskEmail:
		at := strings.Index(v, "@")
		if at <= 1 {
			return "***@***"
		}
		return v[:1] + "***" + v[at:]
	case MaskIDCard:
		if len(v) >= 8 {
			return v[:4] + "**********" + v[len(v)-4:]
		}
		return "********"
	case MaskName:
		r := []rune(v)
		if len(r) <= 1 {
			return "*"
		}
		return string(r[0]) + strings.Repeat("*", len(r)-1)
	case MaskMoney, MaskSecret:
		return "***"
	default:
		return v
	}
}

// MaskRow 按规则对一行数据脱敏（原地修改副本）。
func MaskRow(rules Rules, table string, row map[string]interface{}) map[string]interface{} {
	colRules, ok := rules[table]
	if !ok {
		return row
	}
	for col, mt := range colRules {
		if mt == "" {
			continue // 显式关闭（覆盖平台默认）
		}
		if val, ok := row[col]; ok {
			if s, ok := val.(string); ok && s != "" {
				row[col] = MaskValue(mt, s)
			}
		}
	}
	return row
}

// DefaultRules 默认脱敏规则（与 model 列名对应）。
func DefaultRules() Rules {
	return Rules{
		"customers": {
			"name":    MaskName,
			"phone":   MaskPhone,
			"email":   MaskEmail,
			"id_card": MaskIDCard,
		},
		"users": {
			"password": MaskSecret,
		},
	}
}

// Resolver 从数据库读取脱敏规则，带内存缓存。
// 规则可在运行时可视化修改并立即生效。
type Resolver struct {
	store MaskStore
	mu    sync.RWMutex
	cache map[string]Rules // key: tenantID
}

func NewResolver(store MaskStore) *Resolver {
	return &Resolver{store: store, cache: map[string]Rules{}}
}

// Refresh 从数据库重新加载某租户的脱敏规则到缓存。
func (r *Resolver) Refresh(tenantID string) error {
	m, err := r.store.GetMaskRulesAsMap(tenantID)
	if err != nil {
		return err
	}
	rules := Rules{}
	for table, cols := range m {
		rules[table] = map[string]MaskType{}
		for col, rule := range cols {
			if !rule.Enabled {
				rules[table][col] = "" // 空类型表示显式关闭（覆盖平台默认）
				continue
			}
			rules[table][col] = MaskType(rule.MaskType)
		}
	}
	r.mu.Lock()
	r.cache[tenantID] = rules
	r.mu.Unlock()
	return nil
}

// Rules 返回某租户生效的脱敏规则（缓存/默认回退）。
func (r *Resolver) Rules(tenantID string) Rules {
	r.mu.RLock()
	if rules, ok := r.cache[tenantID]; ok {
		r.mu.RUnlock()
		return rules
	}
	r.mu.RUnlock()
	return DefaultRules()
}

// MaskRow 按租户生效规则对一行数据脱敏。
func (r *Resolver) MaskRow(tenantID, table string, row map[string]interface{}) map[string]interface{} {
	return MaskRow(r.Rules(tenantID), table, row)
}
