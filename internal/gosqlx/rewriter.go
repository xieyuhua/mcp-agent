package gosqlx

import (
	"fmt"
	"sort"
	"strings"
)

// TableRule 后台为某张「原表」配置的行级权限条件。
// Condition 使用占位符 {alias} 代表该表在 SQL 中的有效引用名（别名或表名），
// 例如： {alias}.tenant_id = 't1' AND {alias}.region_id = 'r1'
// 引擎会在注入时把 {alias} 替换为实际别名，自动适配 SQL 中的别名。
type TableRule struct {
	Table     string // 物理表名（大小写不敏感匹配）
	Condition string // where 片段模板，含 {alias} 占位符
}

// injection 描述一次待插入的 where 片段。
type injection struct {
	scope     *SelectScope
	condition string // 已完成别名替换的最终 where 片段
}

// InjectWhere 按后台配置的表规则，为 SQL 中每个作用域里出现的、被管控的表
// 自动合成并注入行级 where 条件。自动适配别名、支持多表关联与子查询。
//
// 返回改写后的 SQL。若某作用域已存在 WHERE，则以 AND 追加；否则新增 WHERE 子句。
func InjectWhere(sql string, rules []TableRule) (string, error) {
	res, err := Parse(sql)
	if err != nil {
		return "", err
	}
	ruleMap := map[string]string{}
	for _, r := range rules {
		if strings.TrimSpace(r.Condition) == "" {
			continue
		}
		ruleMap[strings.ToLower(r.Table)] = r.Condition
	}
	if len(ruleMap) == 0 {
		return sql, nil
	}

	var injections []injection
	for _, sc := range res.Scopes {
		var parts []string
		for _, t := range sc.Tables {
			if t.Name == "" { // 派生表跳过（其内部作用域已单独处理）
				continue
			}
			cond, ok := ruleMap[strings.ToLower(t.Name)]
			if !ok {
				continue
			}
			eff := t.EffectiveName()
			rendered := renderCondition(cond, eff)
			parts = append(parts, "("+rendered+")")
		}
		if len(parts) == 0 {
			continue
		}
		injections = append(injections, injection{
			scope:     sc,
			condition: strings.Join(parts, " AND "),
		})
	}
	if len(injections) == 0 {
		return sql, nil
	}

	return applyInjections(res, injections)
}

// renderCondition 把 {alias} / {table} 占位符替换为实际有效名。
func renderCondition(cond, eff string) string {
	cond = strings.ReplaceAll(cond, "{alias}", eff)
	cond = strings.ReplaceAll(cond, "{table}", eff)
	return cond
}

// applyInjections 基于 token 偏移，从后往前把 where 片段插入原始 SQL 文本，
// 避免前面的插入影响后续偏移。
func applyInjections(res *ParseResult, injs []injection) (string, error) {
	type edit struct {
		at   int    // 插入位置（原始 SQL 字节偏移）
		text string // 插入文本
	}
	var edits []edit

	for _, in := range injs {
		sc := in.scope
		if sc.whereTokenIdx >= 0 {
			// 已有 WHERE：在 WHERE 关键字之后追加 "(cond) AND"
			whereTok := res.Tokens[sc.whereTokenIdx]
			edits = append(edits, edit{
				at:   whereTok.End,
				text: " (" + in.condition + ") AND",
			})
		} else {
			// 无 WHERE：在子句边界前插入 " WHERE (cond)"
			insTok := res.Tokens[sc.clauseEndIdx]
			edits = append(edits, edit{
				at:   insTok.Pos,
				text: " WHERE (" + in.condition + ") ",
			})
		}
	}

	// 按插入位置从大到小排序，倒序插入
	sort.Slice(edits, func(i, j int) bool { return edits[i].at > edits[j].at })

	out := res.SQL
	for _, e := range edits {
		if e.at < 0 || e.at > len(out) {
			return "", fmt.Errorf("注入位置越界: %d", e.at)
		}
		out = out[:e.at] + e.text + out[e.at:]
	}
	return out, nil
}
