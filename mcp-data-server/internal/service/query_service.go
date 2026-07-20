package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/security"
)

// QueryService 查询编排服务：安全校验 -> 隔离查询 -> 审计。
type QueryService struct {
	repo  *repository.QueryRepo
	audit *AuditService
}

func NewQueryService(repo *repository.QueryRepo, audit *AuditService) *QueryService {
	return &QueryService{repo: repo, audit: audit}
}

// ProgressFunc 工具执行期间的进度回调（read=已读取行数，message=提示文本）。
// 由 MCP 传输层转换为 notifications/progress 推送给客户端，实现「分析过程」流式展示。
type ProgressFunc func(read int, message string)

// QueryTable 结构化安全查询。onProgress 非 nil 时，每读取一批数据回调一次进度。
func (s *QueryService) QueryTable(ctx context.Context, t *QueryContext, req repository.QueryRequest, onProgress ProgressFunc) ([]map[string]interface{}, error) {
	if err := security.ValidateFieldList(req.Fields); err != nil {
		return nil, err
	}
	if err := security.ValidateFilters(req.Filters); err != nil {
		return nil, err
	}

	rows, err := s.repo.QueryRows(req)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, _, err := scanRows(rows, onProgress)
	if err != nil {
		return nil, err
	}

	s.writeAudit(t, "query_table", req.Table, toJSON(req), len(out))
	return out, nil
}

// RunSQL 原生 SQL 查询。onProgress 非 nil 时，每读取一批数据回调一次进度。
func (s *QueryService) RunSQL(ctx context.Context, t *QueryContext, sql string, onProgress ProgressFunc) ([]map[string]interface{}, error) {
	if err := security.ValidateSQL(sql); err != nil {
		return nil, err
	}
	rows, err := s.repo.RawSQLRows(sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, _, err := scanRows(rows, onProgress)
	if err != nil {
		return nil, err
	}
	s.writeAudit(t, "run_sql", "", sql, len(out))
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

// DescribeTable 返回表字段信息。
func (s *QueryService) DescribeTable(t *QueryContext, table string) ([]string, error) {
	cols, err := s.repo.DB().Migrator().ColumnTypes(table)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name())
	}
	s.writeAudit(t, "describe_table", table, table, 0)
	return names, nil
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


