package repository

import (
	"database/sql"

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

// QueryRows 执行结构化查询，返回底层 *sql.Rows，便于调用方逐行读取并上报进度。
func (r *QueryRepo) QueryRows(req QueryRequest) (*sql.Rows, error) {
	q := r.db.Table(req.Table)
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
