package service

import (
	"company.com/mcp-data-server/internal/model"

	"gorm.io/gorm"
)

// AuditService 审计服务：记录所有工具调用与数据访问。
type AuditService struct {
	db *gorm.DB
}

func NewAuditService(db *gorm.DB) *AuditService {
	return &AuditService{db: db}
}

// Record 写入一条审计日志（异步可在此改为 goroutine）。
func (s *AuditService) Record(log *model.AuditLog) error {
	return s.db.Create(log).Error
}
