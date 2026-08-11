package gosqlx

import (
	"fmt"
	"strings"
)

// dangerousKeywords 只读查询中绝不允许出现的写/DDL/危险关键字。
var dangerousKeywords = map[string]bool{
	"insert": true, "update": true, "delete": true, "drop": true,
	"alter": true, "create": true, "truncate": true, "grant": true,
	"revoke": true, "exec": true, "execute": true, "merge": true,
	"replace": true, "call": true, "attach": true, "detach": true,
	"pragma": true, "vacuum": true, "sleep": true, "benchmark": true,
	"load_file": true, "outfile": true, "dumpfile": true,
	"information_schema": true,
}

// ValidateOptions SQL 校验选项。
type ValidateOptions struct {
	// AllowedTables 允许访问的物理表白名单（大小写不敏感）。为空表示不限制。
	AllowedTables []string
}

// Validate 对 SQL 做结构化只读安全校验，并返回解析结果供上层复用。
func Validate(sql string, opt ValidateOptions) (*ParseResult, error) {
	toks, hasComment := Tokenize(sql)
	if hasComment {
		return nil, fmt.Errorf("SQL 校验失败：不允许注释")
	}

	// 关键字/标识符级危险词检测（词边界天然由 token 保证，不会误伤 created_at）
	for _, t := range toks {
		if t.Kind == TokenKeyword || t.Kind == TokenIdent {
			if dangerousKeywords[strings.ToLower(t.Value)] {
				return nil, fmt.Errorf("SQL 校验失败：包含禁止的关键字 %q", t.Value)
			}
		}
	}

	res, err := Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("SQL 校验失败：%w", err)
	}
	if !res.IsQuery {
		return nil, fmt.Errorf("SQL 校验失败：仅允许 SELECT 查询")
	}

	if len(opt.AllowedTables) > 0 {
		allow := map[string]bool{}
		for _, a := range opt.AllowedTables {
			allow[strings.ToLower(a)] = true
		}
		for _, name := range res.TableNames() {
			if !allow[strings.ToLower(name)] {
				return nil, fmt.Errorf("SQL 校验失败：无权访问表 %q", name)
			}
		}
	}
	return res, nil
}
