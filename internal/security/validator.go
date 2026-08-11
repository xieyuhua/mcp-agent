package security

import (
	"fmt"
	"regexp"
	"strings"
)

// 危险字符/注释，只要出现就拒绝。
var forbiddenSubstrings = []string{
	";", "--", "/*", "*/", "#",
}

// 危险关键字，只在作为独立 SQL 关键字出现时拒绝（避免误伤如 created_at 等标识符）。
var forbiddenKeywords = []string{
	"drop", "delete", "update", "insert", "alter", "truncate",
	"create", "grant", "revoke", "exec", "execute", "merge",
	"union", "sleep", "benchmark",
}

// 需要正则匹配的危险模式：多词、函数、扩展存储过程等。
var forbiddenPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"into outfile", regexp.MustCompile(`(?i)\binto\s+outfile\b`)},
	{"into dumpfile", regexp.MustCompile(`(?i)\binto\s+dumpfile\b`)},
	{"load_file", regexp.MustCompile(`(?i)\bload_file\b`)},
	{"information_schema", regexp.MustCompile(`(?i)\binformation_schema\b`)},
	{"xp_/sp_", regexp.MustCompile(`(?i)\b[xs]p_[a-z0-9_]+\b`)},
}

// 预编译关键字正则，避免每次校验都重复编译。
var forbiddenKeywordRes = func() []*regexp.Regexp {
	res := make([]*regexp.Regexp, len(forbiddenKeywords))
	for i, kw := range forbiddenKeywords {
		res[i] = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(kw) + `\b`)
	}
	return res
}()

var identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidateSQL 校验原生 SQL：仅允许 SELECT，拦截危险关键字、注释与多语句。
// 关键字采用 \b 词边界匹配，避免把 created_at 这类标识符误判为 create。
func ValidateSQL(sql string) error {
	s := strings.TrimSpace(sql)
	// 去除末尾多余分号（LLM 常生成 SELECT ...; 这种单语句）。
	// 保留对多语句（分号出现在中间）的拦截，例如 SELECT ...; DROP ... 仍会被拒绝。
	for strings.HasSuffix(s, ";") {
		s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	}
	if s == "" {
		return fmt.Errorf("empty sql")
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "select") {
		return fmt.Errorf("only SELECT statements are allowed")
	}

	// 1. 字面危险符号/注释
	for _, f := range forbiddenSubstrings {
		if strings.Contains(lower, f) {
			return fmt.Errorf("forbidden token detected: %q", f)
		}
	}

	// 2. 独立关键字匹配（带词边界，不误伤标识符）
	for i, kw := range forbiddenKeywords {
		if forbiddenKeywordRes[i].MatchString(lower) {
			return fmt.Errorf("forbidden keyword detected: %q", kw)
		}
	}

	// 3. 多词/特殊模式匹配
	for _, p := range forbiddenPatterns {
		if p.re.MatchString(lower) {
			return fmt.Errorf("forbidden pattern detected: %q", p.name)
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
