package repository

import (
	"time"

	"company.com/mcp-data-server/internal/model"

	"gorm.io/gorm"
)

// Seed 写入演示租户、客户与订单数据。
func Seed(db *gorm.DB) error {
	var cnt int64
	db.Model(&model.Tenant{}).Count(&cnt)
	if cnt > 0 {
		return nil
	}

	tenants := []model.Tenant{
		{ID: "t1", Name: "华东零售集团"},
		{ID: "t2", Name: "华南零售集团"},
	}
	if err := db.Create(&tenants).Error; err != nil {
		return err
	}

	now := time.Now()
	customers := []model.Customer{
		{ID: 1, TenantID: "t1", RegionID: "r1", StoreID: "s1", Name: "张三", Phone: "13800001111", Email: "zhangsan@example.com", IDCard: "310000199001011234", CreatedAt: now},
		{ID: 2, TenantID: "t1", RegionID: "r1", StoreID: "s1", Name: "李四", Phone: "13800002222", Email: "lisi@example.com", IDCard: "320000199202022345", CreatedAt: now},
		{ID: 3, TenantID: "t1", RegionID: "r1", StoreID: "s2", Name: "王五", Phone: "13800003333", Email: "wangwu@example.com", IDCard: "330000199303033456", CreatedAt: now},
		{ID: 4, TenantID: "t2", RegionID: "r9", StoreID: "s9", Name: "赵六", Phone: "13900004444", Email: "zhaoliu@example.com", IDCard: "440000199404044567", CreatedAt: now},
	}
	if err := db.Create(&customers).Error; err != nil {
		return err
	}

	orders := []model.Order{
		{ID: 1, TenantID: "t1", RegionID: "r1", StoreID: "s1", CustomerID: 1, Amount: 199.50, Status: "paid", CreatedAt: now},
		{ID: 2, TenantID: "t1", RegionID: "r1", StoreID: "s1", CustomerID: 2, Amount: 299.00, Status: "paid", CreatedAt: now},
		{ID: 3, TenantID: "t1", RegionID: "r1", StoreID: "s2", CustomerID: 3, Amount: 99.90, Status: "refunded", CreatedAt: now},
		{ID: 4, TenantID: "t2", RegionID: "r9", StoreID: "s9", CustomerID: 4, Amount: 599.00, Status: "paid", CreatedAt: now},
	}
	return db.Create(&orders).Error
}
