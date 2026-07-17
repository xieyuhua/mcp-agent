package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/mask"
	"company.com/mcp-data-server/internal/model"
	"company.com/mcp-data-server/internal/repository"
	"company.com/mcp-data-server/internal/security"
	"company.com/mcp-data-server/internal/tenant"
)

// QueryService 查询编排服务：权限校验 -> 安全校验 -> 隔离查询 -> 脱敏 -> 审计。
type QueryService struct {
	repo      *repository.QueryRepo
	audit     *AuditService
	authz     *auth.Resolver
	masker    *mask.Resolver
	maskEnabled bool
}

func NewQueryService(repo *repository.QueryRepo, audit *AuditService, authz *auth.Resolver, masker *mask.Resolver, maskEnabled bool) *QueryService {
	return &QueryService{repo: repo, audit: audit, authz: authz, masker: masker, maskEnabled: maskEnabled}
}

// QueryTable 结构化安全查询（所有角色可用）。
func (s *QueryService) QueryTable(ctx context.Context, t *tenant.Context, req repository.QueryRequest) ([]map[string]interface{}, error) {
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

	// 3. 执行（自动叠加租户/区域/门店隔离）
	rows, err := s.repo.Query(t, s.authz.Scope(t.TenantID, t.Role), req)
	if err != nil {
		return nil, err
	}

	// 4. 数据脱敏
	if s.maskEnabled {
		for i := range rows {
			rows[i] = s.masker.MaskRow(t.TenantID, req.Table, rows[i])
		}
	}

	// 5. 审计
	s.writeAudit(t, "query_table", req.Table, toJSON(req), len(rows))
	return rows, nil
}

// RunSQL 原生 SQL 查询（仅平台运营）。
func (s *QueryService) RunSQL(ctx context.Context, t *tenant.Context, sql string) ([]map[string]interface{}, error) {
	if !s.authz.CanRunRawSQL(t.TenantID, t.Role) {
		return nil, fmt.Errorf("role %q is not allowed to run raw SQL", t.Role)
	}
	if err := security.ValidateSQL(sql); err != nil {
		return nil, err
	}
	rows, err := s.repo.RawSQL(sql)
	if err != nil {
		return nil, err
	}
	// 平台运营不做脱敏，但仍审计
	s.writeAudit(t, "run_sql", "", sql, len(rows))
	return rows, nil
}

// DescribeTable 返回表字段信息（供分析师使用）。
func (s *QueryService) DescribeTable(t *tenant.Context, table string) ([]string, error) {
	if !s.authz.AllowedTables(t.TenantID, t.Role)[table] {
		return nil, fmt.Errorf("role %q is not allowed to access table %q", t.Role, table)
	}
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
