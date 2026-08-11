package gosqlx

import (
	"fmt"
	"strings"
)

// TableRef 一个表引用：物理表名 + 别名（可能为空）。
type TableRef struct {
	Name  string // 物理表名（去引号、原大小写）
	Alias string // 别名；无别名时为空
	Pos   int    // FROM/JOIN 中该表引用的 token 位置（调试用）
}

// EffectiveName 返回在 WHERE 中引用该表列时应使用的限定名：
// 有别名用别名，否则用表名。
func (t TableRef) EffectiveName() string {
	if t.Alias != "" {
		return t.Alias
	}
	return t.Name
}

// SelectScope 一个 SELECT 作用域（顶层查询或子查询）。
// 记录该作用域内的表引用，以及 WHERE 的注入位置信息。
type SelectScope struct {
	Tables []TableRef

	// selectStart 该 SELECT 关键字在 token 流中的下标。
	selectStart int
	// whereTokenIdx WHERE 关键字的 token 下标；-1 表示没有 WHERE。
	whereTokenIdx int
	// clauseEndIdx WHERE 之后各子句（GROUP/ORDER/LIMIT/) 等）的起始 token 下标，
	// 用于「无 WHERE 时」确定注入 WHERE 的插入点。
	clauseEndIdx int
}

// ParseResult 解析结果。
type ParseResult struct {
	Tokens  []Token
	Scopes  []*SelectScope // 所有 SELECT 作用域（含子查询），顺序为出现顺序
	SQL     string
	IsQuery bool // 是否为 SELECT 查询
}

// parser 递归下降解析器，专注于抽取 FROM/JOIN 表结构与 WHERE 边界，
// 不构建完整表达式 AST（对权限注入而言足够，且更稳健）。
type parser struct {
	toks []Token
	pos  int
	res  *ParseResult
}

// Parse 解析 SQL，返回作用域信息。若存在注释或多语句，返回 error。
func Parse(sql string) (*ParseResult, error) {
	toks, hasComment := Tokenize(sql)
	if hasComment {
		return nil, fmt.Errorf("SQL 中不允许出现注释")
	}
	// 多语句检测：分号只允许出现在末尾。
	for i, t := range toks {
		if t.Kind == TokenPunct && t.Value == ";" {
			// 后面若还有非 EOF token，则是多语句
			for j := i + 1; j < len(toks); j++ {
				if toks[j].Kind != TokenEOF {
					return nil, fmt.Errorf("不允许多条 SQL 语句")
				}
			}
		}
	}

	res := &ParseResult{Tokens: toks, SQL: sql}
	p := &parser{toks: toks, res: res}

	// 跳过前导 WITH（CTE 暂不深度解析，只保证 SELECT 主体被解析）
	if p.cur().Kind == TokenKeyword && p.cur().Value == "select" {
		res.IsQuery = true
	} else if p.cur().Kind == TokenKeyword && p.cur().Value == "with" {
		res.IsQuery = true
	}
	if !res.IsQuery {
		return nil, fmt.Errorf("仅支持 SELECT 查询")
	}

	p.parseSelectStatement()
	return res, nil
}

func (p *parser) cur() Token {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return Token{Kind: TokenEOF}
}

func (p *parser) at(i int) Token {
	if i >= 0 && i < len(p.toks) {
		return p.toks[i]
	}
	return Token{Kind: TokenEOF}
}

func (p *parser) isKW(v string) bool {
	t := p.cur()
	return t.Kind == TokenKeyword && t.Value == v
}

func (p *parser) isPunct(v string) bool {
	t := p.cur()
	return t.Kind == TokenPunct && t.Value == v
}

// parseSelectStatement 解析一个（可能带括号的）SELECT，登记其作用域。
// 支持 UNION：把每个 SELECT 作为独立作用域处理。
func (p *parser) parseSelectStatement() {
	for {
		p.parseSingleSelect()
		// 处理 UNION [ALL] 后续 SELECT
		if p.isKW("union") {
			p.pos++
			if p.isKW("all") || p.isKW("distinct") {
				p.pos++
			}
			continue
		}
		break
	}
}

// parseSingleSelect 解析单个 SELECT 主体。
func (p *parser) parseSingleSelect() {
	// 处理前导 WITH（跳过 CTE 定义列表，进入主 SELECT）
	if p.isKW("with") {
		p.skipWith()
	}
	if !p.isKW("select") {
		return
	}
	scope := &SelectScope{selectStart: p.pos, whereTokenIdx: -1, clauseEndIdx: -1}
	p.pos++ // 跳过 select

	// 跳过 select 列表，直到 FROM 或语句结束（列表里可能有子查询，需递归）
	p.skipSelectList()

	if p.isKW("from") {
		p.pos++
		p.parseFromClause(scope)
	}

	// 定位 WHERE 及其后子句边界
	p.locateWhere(scope)

	p.res.Scopes = append(p.res.Scopes, scope)
}

// skipWith 跳过 CTE：with name as ( select ... ) [, name2 as (...)]
func (p *parser) skipWith() {
	p.pos++ // with
	for {
		// name
		if p.cur().Kind == TokenIdent || p.cur().Kind == TokenQuoted {
			p.pos++
		}
		if p.isKW("as") {
			p.pos++
		}
		if p.isPunct("(") {
			// CTE 体本身是子查询，递归解析
			p.parseParenSubquery()
		}
		if p.isPunct(",") {
			p.pos++
			continue
		}
		break
	}
}

// skipSelectList 跳过 select 与 from 之间的投影列表；遇到括号内子查询则递归解析。
func (p *parser) skipSelectList() {
	depth := 0
	for {
		t := p.cur()
		if t.Kind == TokenEOF {
			return
		}
		if depth == 0 && t.Kind == TokenKeyword && t.Value == "from" {
			return
		}
		if t.Kind == TokenPunct && t.Value == "(" {
			// 可能是子查询
			if p.nextIsSelect() {
				p.parseParenSubquery()
				continue
			}
			depth++
			p.pos++
			continue
		}
		if t.Kind == TokenPunct && t.Value == ")" {
			if depth == 0 {
				return
			}
			depth--
		}
		p.pos++
	}
}

// nextIsSelect 判断当前 "(" 后面是否紧跟 SELECT（可能带空白已被词法过滤）。
func (p *parser) nextIsSelect() bool {
	if !p.isPunct("(") {
		return false
	}
	n := p.at(p.pos + 1)
	return n.Kind == TokenKeyword && (n.Value == "select" || n.Value == "with")
}

// parseParenSubquery 解析形如 ( SELECT ... ) 的子查询，递归登记其作用域。
func (p *parser) parseParenSubquery() {
	if !p.isPunct("(") {
		return
	}
	p.pos++ // 跳过 (
	p.parseSelectStatement()
	// 跳过匹配的 )
	if p.isPunct(")") {
		p.pos++
	}
}

// parseFromClause 解析 FROM 后的表引用列表 + JOIN，登记到 scope.Tables。
func (p *parser) parseFromClause(scope *SelectScope) {
	for {
		p.parseTableRef(scope)
		if p.isPunct(",") {
			p.pos++
			continue
		}
		// JOIN 关键字族
		if p.isJoinStart() {
			p.consumeJoinKeywords()
			p.parseTableRef(scope)
			// 跳过 ON <expr> / USING (...)
			p.skipJoinCondition()
			continue
		}
		break
	}
}

func (p *parser) isJoinStart() bool {
	t := p.cur()
	if t.Kind != TokenKeyword {
		return false
	}
	switch t.Value {
	case "join", "inner", "left", "right", "full", "cross":
		return true
	}
	return false
}

func (p *parser) consumeJoinKeywords() {
	for {
		t := p.cur()
		if t.Kind == TokenKeyword {
			switch t.Value {
			case "inner", "left", "right", "full", "cross", "outer", "join":
				p.pos++
				continue
			}
		}
		break
	}
}

func (p *parser) skipJoinCondition() {
	if p.isKW("on") {
		p.pos++
		p.skipExprUntilClauseOrJoin()
	} else if p.isKW("using") {
		p.pos++
		if p.isPunct("(") {
			p.skipParens()
		}
	}
}

// parseTableRef 解析单个表引用：物理表名/子查询 + 可选别名。
func (p *parser) parseTableRef(scope *SelectScope) {
	// 派生表：( SELECT ... ) alias
	if p.nextIsSelect() {
		p.parseParenSubquery()
		alias := p.tryParseAlias()
		if alias != "" {
			scope.Tables = append(scope.Tables, TableRef{Name: "", Alias: alias, Pos: p.pos})
		}
		return
	}

	t := p.cur()
	if t.Kind != TokenIdent && t.Kind != TokenQuoted {
		return
	}
	name := t.Value
	pos := p.pos
	p.pos++
	// schema.table 形式
	if p.isPunct(".") {
		p.pos++
		if p.cur().Kind == TokenIdent || p.cur().Kind == TokenQuoted {
			name = p.cur().Value
			p.pos++
		}
	}
	alias := p.tryParseAlias()
	scope.Tables = append(scope.Tables, TableRef{Name: name, Alias: alias, Pos: pos})
}

// tryParseAlias 尝试解析别名：可选 AS + 标识符（非关键字）。
func (p *parser) tryParseAlias() string {
	if p.isKW("as") {
		p.pos++
		if p.cur().Kind == TokenIdent || p.cur().Kind == TokenQuoted {
			a := p.cur().Value
			p.pos++
			return a
		}
		return ""
	}
	t := p.cur()
	// 裸别名：必须是普通标识符，且不是会终止 FROM 的关键字
	if t.Kind == TokenIdent {
		p.pos++
		return t.Value
	}
	return ""
}

// locateWhere 从当前 scope 的 FROM 之后定位 WHERE 关键字与其后子句边界。
func (p *parser) locateWhere(scope *SelectScope) {
	depth := 0
	i := p.pos
	for i < len(p.toks) {
		t := p.toks[i]
		if t.Kind == TokenEOF {
			break
		}
		if t.Kind == TokenPunct && t.Value == "(" {
			depth++
			i++
			continue
		}
		if t.Kind == TokenPunct && t.Value == ")" {
			if depth == 0 {
				break // 子查询结束
			}
			depth--
			i++
			continue
		}
		if depth == 0 && t.Kind == TokenKeyword {
			switch t.Value {
			case "where":
				scope.whereTokenIdx = i
			case "group", "having", "order", "limit", "offset", "union":
				if scope.clauseEndIdx == -1 {
					scope.clauseEndIdx = i
				}
			}
		}
		if depth == 0 && t.Kind == TokenPunct && t.Value == ";" {
			if scope.clauseEndIdx == -1 {
				scope.clauseEndIdx = i
			}
			break
		}
		i++
	}
	if scope.clauseEndIdx == -1 {
		scope.clauseEndIdx = i
	}
	// 推进主指针到本 select 结束边界，便于外层继续
	p.pos = i
}

// ---- 通用括号 / 表达式跳过 ----

func (p *parser) skipParens() {
	if !p.isPunct("(") {
		return
	}
	depth := 0
	for {
		t := p.cur()
		if t.Kind == TokenEOF {
			return
		}
		if t.Kind == TokenPunct && t.Value == "(" {
			depth++
		} else if t.Kind == TokenPunct && t.Value == ")" {
			depth--
			p.pos++
			if depth == 0 {
				return
			}
			continue
		}
		p.pos++
	}
}

// skipExprUntilClauseOrJoin 跳过表达式，直到遇到会结束当前表引用/条件的关键字。
func (p *parser) skipExprUntilClauseOrJoin() {
	depth := 0
	for {
		t := p.cur()
		if t.Kind == TokenEOF {
			return
		}
		if t.Kind == TokenPunct && t.Value == "(" {
			depth++
			p.pos++
			continue
		}
		if t.Kind == TokenPunct && t.Value == ")" {
			if depth == 0 {
				return
			}
			depth--
			p.pos++
			continue
		}
		if depth == 0 {
			if t.Kind == TokenPunct && (t.Value == "," || t.Value == ";") {
				return
			}
			if t.Kind == TokenKeyword {
				switch t.Value {
				case "where", "group", "having", "order", "limit", "offset",
					"join", "inner", "left", "right", "full", "cross", "union":
					return
				}
			}
		}
		p.pos++
	}
}

// TableNames 返回解析出的全部物理表名（去重，忽略派生表）。
func (r *ParseResult) TableNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, sc := range r.Scopes {
		for _, t := range sc.Tables {
			if t.Name == "" {
				continue
			}
			key := strings.ToLower(t.Name)
			if !seen[key] {
				seen[key] = true
				out = append(out, t.Name)
			}
		}
	}
	return out
}
