package repository

import (
	"database/sql"

	"company.com/mcp-data-server/internal/auth"
	"company.com/mcp-data-server/internal/tenant"
	"gorm.io/gorm"
)

// QueryRequest 结构化查询请求（安全、参数化）。
type QueryRequest struct {
	Table   string                 // 目标表
	Fields  []string               // 返回字段，空表示全部
	Filters map[string]interface{} // 等值过滤条件（列=值）
	Order   string                 // 排序，如 "created_at desc"
	Limit   int                    // 上限，默认 100，最大 1000
	Offset  int
}

// QueryRepo 数据访问层，负责行级隔离与参数化查询。
type QueryRepo struct {
	db *gorm.DB
}

func NewQueryRepo(db *gorm.DB) *QueryRepo { return &QueryRepo{db: db} }

// DB 暴露底层 *gorm.DB（供迁移/描述表使用）。
func (r *QueryRepo) DB() *gorm.DB { return r.db }

// applyScope 根据数据范围追加行级隔离条件，实现数据隔离。
// scope 由上层（service）从权限配置解析后传入，repository 不感知配置来源。
func applyScope(q *gorm.DB, t *tenant.Context, scope auth.DataScope) *gorm.DB {
	switch scope {
	case auth.ScopeAll:
		// 平台运营：无限制
	case auth.ScopeTenant:
		q = q.Where("tenant_id = ?", t.TenantID)
	case auth.ScopeRegion:
		q = q.Where("tenant_id = ? AND region_id = ?", t.TenantID, t.RegionID)
	case auth.ScopeStore:
		q = q.Where("tenant_id = ? AND store_id = ?", t.TenantID, t.StoreID)
	}
	return q
}

// Query 执行结构化查询，自动叠加租户/区域/门店隔离条件。
// scope 为调用方解析出的数据可见范围。
func (r *QueryRepo) Query(t *tenant.Context, scope auth.DataScope, req QueryRequest) ([]map[string]interface{}, error) {
	q := r.db.Table(req.Table)
	q = applyScope(q, t, scope)

	if len(req.Fields) > 0 {
		q = q.Select(req.Fields)
	}
	for col, val := range req.Filters {
		// 列名已在校验层确认安全；值使用参数化绑定，杜绝注入
		q = q.Where(col+" = ?", val)
	}
	if req.Order != "" {
		q = q.Order(req.Order)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	q = q.Limit(limit).Offset(req.Offset)

	var rows []map[string]interface{}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// RawSQL 执行原生 SQL（仅平台运营调用，且已通过安全校验）。
func (r *QueryRepo) RawSQL(sql string) ([]map[string]interface{}, error) {
	var rows []map[string]interface{}
	if err := r.db.Raw(sql).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// QueryRows 与 Query 同语义，但返回底层 *sql.Rows，便于调用方逐行读取并上报进度
// （实现「分析过程」流式输出，避免大结果集一次性返回时前端长时间无反馈）。
func (r *QueryRepo) QueryRows(t *tenant.Context, scope auth.DataScope, req QueryRequest) (*sql.Rows, error) {
	q := r.db.Table(req.Table)
	q = applyScope(q, t, scope)
	if len(req.Fields) > 0 {
		q = q.Select(req.Fields)
	}
	for col, val := range req.Filters {
		q = q.Where(col+" = ?", val)
	}
	if req.Order != "" {
		q = q.Order(req.Order)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	q = q.Limit(limit).Offset(req.Offset)
	return q.Rows()
}

// RawSQLRows 与 RawSQL 同语义，返回底层 *sql.Rows 以支持逐行流式读取。
func (r *QueryRepo) RawSQLRows(sql string) (*sql.Rows, error) {
	return r.db.Raw(sql).Rows()
}
