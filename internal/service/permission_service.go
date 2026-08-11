package service

import (
	"fmt"
	"strings"

	"company.com/mcp-data-server/internal/gosqlx"
	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/repository"
)

// PermissionService 权限编排服务：
//   - 依据 agent 传入的 origin_role，从后台配置合成行级 where 并注入 SQL（gosqlx）
//   - 权限校验（表白名单）+ SQL 安全校验
//   - 字段级可见性 / 脱敏
//   - 向大模型提供后台配置的表结构（表注释、字段注释、关联关系），无需直连数据库
type PermissionService struct {
	repo *repository.PermissionRepo
}

func NewPermissionService(repo *repository.PermissionRepo) *PermissionService {
	return &PermissionService{repo: repo}
}

// SuperRole 超级管理员角色码，不受行级/字段权限限制。
const SuperRole = "super_admin"

// FieldPolicy 某角色对某表的字段可见性与脱敏规则。
type FieldPolicy struct {
	Hidden map[string]bool // 不可见字段
	Masked map[string]bool // 需脱敏字段
}

// EnforceResult SQL 权限处理结果。
type EnforceResult struct {
	SQL         string        // 注入权限 where 后的最终 SQL
	Tables      []string      // SQL 涉及的物理表
	FieldPolicy map[string]FieldPolicy // 表 -> 字段策略（用于结果脱敏）
}

// allowedTables 返回后台已启用、对 agent 开放的表名列表。
func (s *PermissionService) allowedTables() ([]string, error) {
	tables, err := s.repo.ListEnabledTables()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tables))
	for _, t := range tables {
		out = append(out, t.Name)
	}
	return out, nil
}

// EnforceSQL 对 agent 传入的原生 SQL 执行完整权限处理：
//  1. gosqlx 结构化安全校验（仅 SELECT、无危险词、表在白名单内）
//  2. 按 role 从后台策略合成行级 where 并注入（自动适配别名、多表、子查询）
//  3. 返回最终 SQL 与字段脱敏策略
func (s *PermissionService) EnforceSQL(role, sql string) (*EnforceResult, error) {
	if strings.TrimSpace(role) == "" {
		return nil, fmt.Errorf("缺少 origin_role，拒绝执行")
	}
	allowed, err := s.allowedTables()
	if err != nil {
		return nil, err
	}
	// 校验（super_admin 也校验只读与语法，但不限制表白名单）
	opt := gosqlx.ValidateOptions{}
	if role != SuperRole {
		opt.AllowedTables = allowed
	}
	parsed, err := gosqlx.Validate(sql, opt)
	if err != nil {
		return nil, err
	}
	tables := parsed.TableNames()

	finalSQL := sql
	if role != SuperRole {
		rules, err := s.buildTableRules(role)
		if err != nil {
			return nil, err
		}
		finalSQL, err = gosqlx.InjectWhere(sql, rules)
		if err != nil {
			return nil, fmt.Errorf("权限条件注入失败: %w", err)
		}
	}

	fp, err := s.buildFieldPolicies(role, tables)
	if err != nil {
		return nil, err
	}

	return &EnforceResult{SQL: finalSQL, Tables: tables, FieldPolicy: fp}, nil
}

// EnforceTableQuery 对结构化 query_table 请求执行权限处理：
// 校验表是否开放、字段是否越权，并把行级 where 转成过滤条件（附加到原 SQL 拼装前）。
// 返回：允许查询的表、追加的 where 片段（已渲染，别名即表名）、字段策略。
func (s *PermissionService) EnforceTableQuery(role, table string, fields []string) (whereClauses []string, fp FieldPolicy, err error) {
	if strings.TrimSpace(role) == "" {
		return nil, fp, fmt.Errorf("缺少 origin_role，拒绝执行")
	}
	if role != SuperRole {
		allowed, e := s.allowedTables()
		if e != nil {
			return nil, fp, e
		}
		if !containsFold(allowed, table) {
			return nil, fp, fmt.Errorf("无权访问表 %q", table)
		}
	}

	policies, fpMap, err := s.tableRulesAndFields(role, table)
	if err != nil {
		return nil, fp, err
	}
	fp = fpMap
	// 字段越权校验
	for _, f := range fields {
		if fp.Hidden[strings.ToLower(f)] {
			return nil, fp, fmt.Errorf("无权查询字段 %q", f)
		}
	}
	// 结构化查询中表无别名，占位符渲染为表名
	for _, p := range policies {
		whereClauses = append(whereClauses, gosqlxRender(p, table))
	}
	return whereClauses, fp, nil
}

// buildTableRules 汇总某角色对所有相关表的行级 where 规则。
func (s *PermissionService) buildTableRules(role string) ([]gosqlx.TableRule, error) {
	policies, err := s.repo.EnabledPoliciesByRole(role)
	if err != nil {
		return nil, err
	}
	// 同一张表可能配置多条，用 AND 合并
	byTable := map[string][]string{}
	for _, p := range policies {
		if strings.TrimSpace(p.Condition) == "" {
			continue
		}
		byTable[p.TableName] = append(byTable[p.TableName], "("+p.Condition+")")
	}
	var rules []gosqlx.TableRule
	for tbl, conds := range byTable {
		rules = append(rules, gosqlx.TableRule{Table: tbl, Condition: strings.Join(conds, " AND ")})
	}
	return rules, nil
}

// tableRulesAndFields 返回某表针对某角色的行级条件模板与字段策略。
func (s *PermissionService) tableRulesAndFields(role, table string) ([]model.RolePolicy, FieldPolicy, error) {
	fp := FieldPolicy{Hidden: map[string]bool{}, Masked: map[string]bool{}}
	var policies []model.RolePolicy
	if role != SuperRole {
		ps, err := s.repo.EnabledPoliciesByRole(role)
		if err != nil {
			return nil, fp, err
		}
		for _, p := range ps {
			if strings.EqualFold(p.TableName, table) {
				policies = append(policies, p)
			}
		}
	}
	fpMap, err := s.buildFieldPolicies(role, []string{table})
	if err != nil {
		return nil, fp, err
	}
	if v, ok := fpMap[strings.ToLower(table)]; ok {
		fp = v
	}
	return policies, fp, nil
}

// buildFieldPolicies 构建各表的字段可见/脱敏策略。
// 规则：
//   - super_admin 无限制
//   - RoleFieldGrant 中 visible=false -> 隐藏；masked=true -> 脱敏
//   - 未显式配置的敏感字段（FieldConfig.Sensitive）默认脱敏
func (s *PermissionService) buildFieldPolicies(role string, tables []string) (map[string]FieldPolicy, error) {
	out := map[string]FieldPolicy{}
	if role == SuperRole {
		return out, nil
	}
	for _, tbl := range tables {
		fp := FieldPolicy{Hidden: map[string]bool{}, Masked: map[string]bool{}}

		// 敏感字段默认脱敏
		fields, err := s.repo.ListFields(tbl)
		if err != nil {
			return nil, err
		}
		for _, f := range fields {
			if f.Sensitive {
				fp.Masked[strings.ToLower(f.Name)] = true
			}
		}

		// 角色字段权限覆盖
		grants, err := s.repo.ListFieldGrants(role, tbl)
		if err != nil {
			return nil, err
		}
		for _, g := range grants {
			key := strings.ToLower(g.Field)
			if !g.Visible {
				fp.Hidden[key] = true
				delete(fp.Masked, key)
			} else if g.Masked {
				fp.Masked[key] = true
			} else {
				delete(fp.Masked, key)
			}
		}
		out[strings.ToLower(tbl)] = fp
	}
	return out, nil
}

// ApplyFieldPolicy 对查询结果按字段策略做隐藏与脱敏。
// tableHint 为结果对应的主表名（结构化查询已知；原生 SQL 多表时按所有表策略合并处理）。
func ApplyFieldPolicy(rows []map[string]interface{}, policies map[string]FieldPolicy) []map[string]interface{} {
	if len(policies) == 0 || len(rows) == 0 {
		return rows
	}
	// 合并所有表策略（按列名，跨表列名冲突时以「隐藏优先、其次脱敏」处理）
	hidden := map[string]bool{}
	masked := map[string]bool{}
	for _, fp := range policies {
		for k := range fp.Hidden {
			hidden[k] = true
		}
		for k := range fp.Masked {
			masked[k] = true
		}
	}
	for _, row := range rows {
		for col := range row {
			lc := strings.ToLower(col)
			if hidden[lc] {
				delete(row, col)
				continue
			}
			if masked[lc] {
				row[col] = maskValue(row[col])
			}
		}
	}
	return rows
}

// maskValue 对单个值做脱敏：保留首尾少量字符，中间以 * 替代。
func maskValue(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	s := fmt.Sprintf("%v", v)
	r := []rune(s)
	n := len(r)
	switch {
	case n == 0:
		return ""
	case n <= 2:
		return strings.Repeat("*", n)
	case n <= 6:
		return string(r[0]) + strings.Repeat("*", n-2) + string(r[n-1])
	default:
		return string(r[:2]) + strings.Repeat("*", n-4) + string(r[n-2:])
	}
}

// SchemaForLLM 向大模型输出后台配置的表结构（不直连数据库）。
// 只返回启用中的表，含表注释、字段注释、关联关系。
type SchemaTable struct {
	Name      string              `json:"name"`
	Title     string              `json:"title"`
	Comment   string              `json:"comment"`
	Columns   []SchemaColumn      `json:"columns"`
	Relations []model.TableRelation `json:"relations,omitempty"`
}

type SchemaColumn struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	DataType string `json:"data_type"`
	Comment  string `json:"comment"`
}

// GetSchema 返回供大模型使用的表结构元数据。role 用于过滤角色不可见字段。
func (s *PermissionService) GetSchema(role string) ([]SchemaTable, error) {
	tables, err := s.repo.ListEnabledTables()
	if err != nil {
		return nil, err
	}
	relations, err := s.repo.ListRelations()
	if err != nil {
		return nil, err
	}
	relByTable := map[string][]model.TableRelation{}
	for _, r := range relations {
		relByTable[strings.ToLower(r.LeftTable)] = append(relByTable[strings.ToLower(r.LeftTable)], r)
	}

	var fpMap map[string]FieldPolicy
	if role != "" && role != SuperRole {
		names := make([]string, 0, len(tables))
		for _, t := range tables {
			names = append(names, t.Name)
		}
		fpMap, _ = s.buildFieldPolicies(role, names)
	}

	var out []SchemaTable
	for _, t := range tables {
		fields, err := s.repo.ListFields(t.Name)
		if err != nil {
			return nil, err
		}
		st := SchemaTable{Name: t.Name, Title: t.Title, Comment: t.Comment}
		fp := fpMap[strings.ToLower(t.Name)]
		for _, f := range fields {
			if fp.Hidden != nil && fp.Hidden[strings.ToLower(f.Name)] {
				continue // 角色不可见字段不暴露给大模型
			}
			cmt := f.Comment
			if fp.Masked != nil && fp.Masked[strings.ToLower(f.Name)] {
				cmt = strings.TrimSpace(cmt + " (返回时脱敏)")
			}
			st.Columns = append(st.Columns, SchemaColumn{
				Name: f.Name, Title: f.Title, DataType: f.DataType, Comment: cmt,
			})
		}
		st.Relations = relByTable[strings.ToLower(t.Name)]
		out = append(out, st)
	}
	return out, nil
}

// ---- helpers ----

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// gosqlxRender 把行级策略模板中的占位符渲染为具体的有效名（结构化查询用表名）。
func gosqlxRender(p model.RolePolicy, eff string) string {
	c := strings.ReplaceAll(p.Condition, "{alias}", eff)
	c = strings.ReplaceAll(c, "{table}", eff)
	return c
}
