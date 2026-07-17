package security

import (
	"fmt"
	"regexp"
	"strings"
)

// 禁止的 SQL 片段，用于防注入与防破坏性操作。
var forbiddenTokens = []string{
	";", "--", "/*", "*/", "#",
	"drop", "delete", "update", "insert", "alter", "truncate",
	"create", "grant", "revoke", "exec", "execute", "merge",
	"into outfile", "into dumpfile", "load_file", "sleep", "benchmark",
	"union", "xp_", "sp_", "information_schema",
}

var identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidateSQL 校验原生 SQL：仅允许 SELECT，拦截危险关键字与多语句。
func ValidateSQL(sql string) error {
	s := strings.TrimSpace(sql)
	if s == "" {
		return fmt.Errorf("empty sql")
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "select") {
		return fmt.Errorf("only SELECT statements are allowed")
	}
	for _, f := range forbiddenTokens {
		if strings.Contains(lower, f) {
			return fmt.Errorf("forbidden token detected: %q", f)
		}
	}
	return nil
}

// ValidIdentifier 校验列名/表名是否为安全标识符，防止列名注入。
func ValidIdentifier(name string) bool {
	return identRe.MatchString(name)
}

// ValidateFieldList 校验查询字段列表。
func ValidateFieldList(fields []string) error {
	for _, f := range fields {
		if !ValidIdentifier(f) {
			return fmt.Errorf("invalid field name: %q", f)
		}
	}
	return nil
}

// ValidateFilters 校验过滤条件键（列名）合法性。
func ValidateFilters(filters map[string]interface{}) error {
	for k := range filters {
		if !ValidIdentifier(k) {
			return fmt.Errorf("invalid filter column: %q", k)
		}
	}
	return nil
}
