package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/mask"
	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/security"
	"company.com/mcp-data-server/internal/tenant"
)

// tableNameRe 用于从 SQL 中提取 FROM/JOIN 后的表名（简单正则，满足常见场景）。
var tableNameRe = regexp.MustCompile(`(?i)(?:from|join)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

// QueryService 查询编排服务：权限校验 -> 安全校验 -> 隔离查询 -> 脱敏/字段过滤 -> 审计。
type QueryService struct {
	repo        *repository.QueryRepo
	audit       *AuditService
	authz       *auth.Resolver
	masker      *mask.Resolver
	maskEnabled bool
}

func NewQueryService(repo *repository.QueryRepo, audit *AuditService, authz *auth.Resolver, masker *mask.Resolver, maskEnabled bool) *QueryService {
	return &QueryService{repo: repo, audit: audit, authz: authz, masker: masker, maskEnabled: maskEnabled}
}

// ProgressFunc 工具执行期间的进度回调（read=已读取行数，message=提示文本）。
// 由 MCP 传输层转换为 notifications/progress 推送给客户端，实现「分析过程」流式展示。
type ProgressFunc func(read int, message string)

// QueryTable 结构化安全查询（所有角色可用）。onProgress 非 nil 时，每读取一批数据回调一次进度。
func (s *QueryService) QueryTable(ctx context.Context, t *tenant.Context, req repository.QueryRequest, onProgress ProgressFunc) ([]map[string]interface{}, error) {
	// 1. 表级权限
	if !s.authz.AllowedTables(t.TenantID, t.Role)[req.Table] {
		return nil, fmt.Errorf("role %q is not allowed to access table %q", t.Role, req.Table)
	}
	// 2. 字段/过滤条件安全校验（防列名注入）
	if err := security.ValidateFieldList(req.Fields); err != nil {
		return nil, err
	}
	if err := security.ValidateFilters(req.Filters); err != nil {
		return nil, err
	}

	// 3. 字段级权限：显式请求/过滤隐藏字段直接拒绝
	hidden := s.authz.HiddenFields(t.TenantID, t.Role)[req.Table]
	for _, f := range req.Fields {
		if hidden[f] {
			return nil, fmt.Errorf("field %q is hidden for role %q", f, t.Role)
		}
	}
	for k := range req.Filters {
		if hidden[k] {
			return nil, fmt.Errorf("filter column %q is hidden for role %q", k, t.Role)
		}
	}

	// 4. 执行（自动叠加租户/区域/门店隔离），逐行读取并上报进度
	rows, err := s.repo.QueryRows(t, s.authz.Scope(t.TenantID, t.Role), req)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	maskFn := func(row map[string]interface{}) map[string]interface{} {
		if s.maskEnabled {
			row = s.masker.MaskRow(t.TenantID, req.Table, row)
		}
		return filterRowHiddenFields(row, hidden)
	}
	out, _, err := scanRows(rows, maskFn, onProgress)
	if err != nil {
		return nil, err
	}

	// 5. 审计
	s.writeAudit(t, "query_table", req.Table, toJSON(req), len(out))
	return out, nil
}

// RunSQL 原生 SQL 查询（仅平台运营）。onProgress 非 nil 时，每读取一批数据回调一次进度。
func (s *QueryService) RunSQL(ctx context.Context, t *tenant.Context, sql string, onProgress ProgressFunc) ([]map[string]interface{}, error) {
	if !s.authz.CanRunRawSQL(t.TenantID, t.Role) {
		return nil, fmt.Errorf("role %q is not allowed to run raw SQL", t.Role)
	}
	if err := security.ValidateSQL(sql); err != nil {
		return nil, err
	}
	rows, err := s.repo.RawSQLRows(sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// 按 SQL 中涉及的表聚合隐藏字段；SELECT * 等场景也按列名过滤
	hidden := s.hiddenFieldsFromSQL(t.TenantID, t.Role, sql)
	maskFn := func(row map[string]interface{}) map[string]interface{} {
		return filterRowHiddenFields(row, hidden)
	}
	out, read, err := scanRows(rows, maskFn, onProgress)
	if err != nil {
		return nil, err
	}
	s.writeAudit(t, "run_sql", "", sql, len(out))
	_ = read
	return out, nil
}

// scanRows 逐行扫描 *sql.Rows 为 []map，并对每行可选脱敏/字段过滤；每读取 progressStep 行回调一次 onProgress。
// []byte 统一转 string，保证后续 JSON 序列化正确（避免 base64 编码）。
func scanRows(rows *sql.Rows, mask func(row map[string]interface{}) map[string]interface{}, onProgress ProgressFunc) ([]map[string]interface{}, int, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}
	out := make([]map[string]interface{}, 0, 64)
	read := 0
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, read, err
		}
		row := make(map[string]interface{}, len(cols))
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[c] = v
		}
		if mask != nil {
			row = mask(row)
		}
		out = append(out, row)
		read++
		if onProgress != nil && read%200 == 0 {
			onProgress(read, fmt.Sprintf("已读取 %d 行", read))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, read, err
	}
	if onProgress != nil && read > 0 {
		onProgress(read, fmt.Sprintf("查询完成，共 %d 行", read))
	}
	return out, read, nil
}

// DescribeTable 返回表字段信息（供分析师使用），隐藏字段不会出现在 schema 中。
func (s *QueryService) DescribeTable(t *tenant.Context, table string) ([]string, error) {
	if !s.authz.AllowedTables(t.TenantID, t.Role)[table] {
		return nil, fmt.Errorf("role %q is not allowed to access table %q", t.Role, table)
	}
	cols, err := s.repo.DB().Migrator().ColumnTypes(table)
	if err != nil {
		return nil, err
	}
	hidden := s.authz.HiddenFields(t.TenantID, t.Role)[table]
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		if hidden[c.Name()] {
			continue
		}
		names = append(names, c.Name())
	}
	s.writeAudit(t, "describe_table", table, table, 0)
	return names, nil
}

func (s *QueryService) writeAudit(t *tenant.Context, action, table, query string, rows int) {
	_ = s.audit.Record(&model.AuditLog{
		TenantID:  t.TenantID,
		UserID:    t.UserID,
		Action:    action,
		Tool:      action,
		TableName: table,
		Query:     query,
		RowCount:  rows,
		IP:        "mcp",
		CreatedAt: time.Now(),
	})
}

func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// filterRowHiddenFields 从单行结果中剔除隐藏字段。
func filterRowHiddenFields(row map[string]interface{}, hidden map[string]bool) map[string]interface{} {
	if len(hidden) == 0 {
		return row
	}
	out := make(map[string]interface{}, len(row))
	for k, v := range row {
		if hidden[k] {
			continue
		}
		out[k] = v
	}
	return out
}

// hiddenFieldsFromSQL 从 SQL 中提取涉及的表名，合并这些表的隐藏字段集合。
// 对于带表前缀的列名（如 customers.id），取短列名后再按全局隐藏集合过滤。
func (s *QueryService) hiddenFieldsFromSQL(tenantID, role, sql string) map[string]bool {
	tables := extractSQLTableNames(sql)
	if len(tables) == 0 {
		return nil
	}
	allHidden := s.authz.HiddenFields(tenantID, role)
	merged := map[string]bool{}
	for _, t := range tables {
		for c, hidden := range allHidden[t] {
			if hidden {
				merged[c] = true
			}
		}
	}
	return merged
}

func extractSQLTableNames(sql string) []string {
	matches := tableNameRe.FindAllStringSubmatch(sql, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		t := strings.TrimSpace(m[1])
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
