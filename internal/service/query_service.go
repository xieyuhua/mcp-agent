package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/security"
)

// QueryService 查询编排服务：权限合成/校验 -> 隔离查询 -> 字段脱敏 -> 审计。
type QueryService struct {
	repo  *repository.QueryRepo
	audit *AuditService
	perm  *PermissionService
}

func NewQueryService(repo *repository.QueryRepo, audit *AuditService, perm *PermissionService) *QueryService {
	return &QueryService{repo: repo, audit: audit, perm: perm}
}

// ProgressFunc 工具执行期间的进度回调（read=已读取行数，message=提示文本）。
// 由 MCP 传输层转换为 notifications/progress 推送给客户端，实现「分析过程」流式展示。
type ProgressFunc func(read int, message string)

// QueryTable 结构化安全查询。按 origin_role 校验表/字段权限，注入行级 where，并对结果脱敏。
func (s *QueryService) QueryTable(ctx context.Context, t *QueryContext, req repository.QueryRequest, onProgress ProgressFunc) ([]map[string]interface{}, error) {
	if err := security.ValidateFieldList(req.Fields); err != nil {
		return nil, err
	}
	if err := security.ValidateFilters(req.Filters); err != nil {
		return nil, err
	}

	// 权限：表白名单校验、字段越权校验、行级 where 注入、字段策略
	whereClauses, fp, err := s.perm.EnforceTableQuery(t.Role, req.Table, req.Fields)
	if err != nil {
		return nil, err
	}
	req.WhereClauses = whereClauses

	rows, err := s.repo.QueryRows(req)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, _, err := scanRows(rows, onProgress)
	if err != nil {
		return nil, err
	}
	// 结果脱敏 / 隐藏字段
	out = ApplyFieldPolicy(out, map[string]FieldPolicy{req.Table: fp})

	s.writeAudit(t, "query_table", req.Table, toJSON(req), len(out))
	return out, nil
}

// RunSQL 原生 SQL 查询。按 origin_role 做 SQL 校验、行级 where 注入（自动适配别名/多表/子查询）、结果脱敏。
func (s *QueryService) RunSQL(ctx context.Context, t *QueryContext, sql string, onProgress ProgressFunc) ([]map[string]interface{}, error) {
	// gosqlx 权限处理：校验 + 合成注入
	enforced, err := s.perm.EnforceSQL(t.Role, sql)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.RawSQLRows(enforced.SQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, _, err := scanRows(rows, onProgress)
	if err != nil {
		return nil, err
	}
	out = ApplyFieldPolicy(out, enforced.FieldPolicy)
	s.writeAudit(t, "run_sql", strings.Join(enforced.Tables, ","), enforced.SQL, len(out))
	return out, nil
}

// scanRows 逐行扫描 *sql.Rows 为 []map，每读取 progressStep 行回调一次 onProgress。
// []byte 统一转 string，保证后续 JSON 序列化正确（避免 base64 编码）。
func scanRows(rows *sql.Rows, onProgress ProgressFunc) ([]map[string]interface{}, int, error) {
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

// DescribeTable 返回后台配置的表结构（表注释 + 字段注释），供大模型理解语义。
// 不再直连数据库探测 schema，而是使用后台预配置的元数据，并按角色过滤不可见字段。
func (s *QueryService) DescribeTable(t *QueryContext, table string) (interface{}, error) {
	schema, err := s.perm.GetSchema(t.Role)
	if err != nil {
		return nil, err
	}
	for _, st := range schema {
		if strings.EqualFold(st.Name, table) {
			s.writeAudit(t, "describe_table", table, table, 0)
			return st, nil
		}
	}
	return nil, fmt.Errorf("表 %q 不存在或未开放", table)
}

// ListSchema 返回全部已开放表的结构元数据（供大模型一次性了解可用数据）。
func (s *QueryService) ListSchema(t *QueryContext) (interface{}, error) {
	schema, err := s.perm.GetSchema(t.Role)
	if err != nil {
		return nil, err
	}
	s.writeAudit(t, "list_schema", "", "", len(schema))
	return schema, nil
}

func (s *QueryService) writeAudit(t *QueryContext, action, table, query string, rows int) {
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


