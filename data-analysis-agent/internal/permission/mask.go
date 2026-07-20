package permission

import (
	"strings"
	"sync"
)

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
			continue
		}
		if val, ok := row[col]; ok {
			if s, ok := val.(string); ok && s != "" {
				row[col] = MaskValue(mt, s)
			}
		}
	}
	return row
}

// DefaultRules 默认脱敏规则。
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

// MaskStore 脱敏规则存储接口。
type MaskStore interface {
	GetMaskRulesAsMap(tenantID string) (map[string]map[string]MaskRule, error)
}

// MaskResolver 从数据库读取脱敏规则，带内存缓存。
type MaskResolver struct {
	store MaskStore
	mu    sync.RWMutex
	cache map[string]Rules
}

func NewMaskResolver(store MaskStore) *MaskResolver {
	return &MaskResolver{store: store, cache: map[string]Rules{}}
}

// Refresh 从数据库重新加载某租户的脱敏规则。
func (r *MaskResolver) Refresh(tenantID string) error {
	m, err := r.store.GetMaskRulesAsMap(tenantID)
	if err != nil {
		return err
	}
	rules := Rules{}
	for table, cols := range m {
		rules[table] = map[string]MaskType{}
		for col, rule := range cols {
			if !rule.Enabled {
				rules[table][col] = ""
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

// Rules 返回某租户生效的脱敏规则。
func (r *MaskResolver) Rules(tenantID string) Rules {
	r.mu.RLock()
	if rules, ok := r.cache[tenantID]; ok {
		r.mu.RUnlock()
		return rules
	}
	r.mu.RUnlock()
	return DefaultRules()
}

// MaskRow 按租户生效规则对一行数据脱敏。
func (r *MaskResolver) MaskRow(tenantID, table string, row map[string]interface{}) map[string]interface{} {
	return MaskRow(r.Rules(tenantID), table, row)
}
