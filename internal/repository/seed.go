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
	if err := db.Create(&orders).Error; err != nil {
		return err
	}
	return seedPermissions(db)
}

// seedPermissions 写入演示用的角色、表结构元数据、字段、关联关系与权限策略。
func seedPermissions(db *gorm.DB) error {
	var cnt int64
	db.Model(&model.Role{}).Count(&cnt)
	if cnt > 0 {
		return nil
	}

	roles := []model.Role{
		{Code: "super_admin", Name: "超级管理员", Remark: "不受行级权限限制"},
		{Code: "tenant_t1_admin", Name: "华东租户管理员", Remark: "仅可见租户 t1 数据"},
		{Code: "store_s1_staff", Name: "s1 门店店员", Remark: "仅可见 t1/r1/s1 数据，敏感字段脱敏"},
	}
	if err := db.Create(&roles).Error; err != nil {
		return err
	}

	tables := []model.TableConfig{
		{Name: "customers", Title: "客户表", Comment: "存储客户基础信息。tenant_id 租户、region_id 大区、store_id 门店，用于数据隔离。", Enabled: true},
		{Name: "orders", Title: "订单表", Comment: "存储订单信息，customer_id 关联 customers.id，amount 金额，status 状态(paid/refunded)。", Enabled: true},
	}
	if err := db.Create(&tables).Error; err != nil {
		return err
	}

	fields := []model.FieldConfig{
		{TableName: "customers", Name: "id", Title: "客户ID", DataType: "int", Comment: "主键"},
		{TableName: "customers", Name: "tenant_id", Title: "租户ID", DataType: "varchar", Comment: "数据隔离字段"},
		{TableName: "customers", Name: "region_id", Title: "大区ID", DataType: "varchar", Comment: "数据隔离字段"},
		{TableName: "customers", Name: "store_id", Title: "门店ID", DataType: "varchar", Comment: "数据隔离字段"},
		{TableName: "customers", Name: "name", Title: "客户姓名", DataType: "varchar", Comment: "客户名称"},
		{TableName: "customers", Name: "phone", Title: "手机号", DataType: "varchar", Comment: "联系电话", Sensitive: true},
		{TableName: "customers", Name: "email", Title: "邮箱", DataType: "varchar", Comment: "电子邮箱", Sensitive: true},
		{TableName: "customers", Name: "id_card", Title: "身份证号", DataType: "varchar", Comment: "身份证件号", Sensitive: true},
		{TableName: "orders", Name: "id", Title: "订单ID", DataType: "int", Comment: "主键"},
		{TableName: "orders", Name: "tenant_id", Title: "租户ID", DataType: "varchar", Comment: "数据隔离字段"},
		{TableName: "orders", Name: "customer_id", Title: "客户ID", DataType: "int", Comment: "关联 customers.id"},
		{TableName: "orders", Name: "amount", Title: "金额", DataType: "decimal", Comment: "订单金额"},
		{TableName: "orders", Name: "status", Title: "状态", DataType: "varchar", Comment: "paid 已支付 / refunded 已退款"},
	}
	if err := db.Create(&fields).Error; err != nil {
		return err
	}

	relations := []model.TableRelation{
		{LeftTable: "orders", LeftColumn: "customer_id", RightTable: "customers", RightColumn: "id", JoinType: "INNER", Comment: "订单归属客户"},
	}
	if err := db.Create(&relations).Error; err != nil {
		return err
	}

	policies := []model.RolePolicy{
		{Role: "tenant_t1_admin", TableName: "customers", Condition: "{alias}.tenant_id = 't1'", Enabled: true},
		{Role: "tenant_t1_admin", TableName: "orders", Condition: "{alias}.tenant_id = 't1'", Enabled: true},
		{Role: "store_s1_staff", TableName: "customers", Condition: "{alias}.tenant_id = 't1' AND {alias}.region_id = 'r1' AND {alias}.store_id = 's1'", Enabled: true},
		{Role: "store_s1_staff", TableName: "orders", Condition: "{alias}.tenant_id = 't1' AND {alias}.region_id = 'r1' AND {alias}.store_id = 's1'", Enabled: true},
	}
	if err := db.Create(&policies).Error; err != nil {
		return err
	}

	grants := []model.RoleFieldGrant{
		// 店员看不到身份证，手机号脱敏
		{Role: "store_s1_staff", TableName: "customers", Field: "id_card", Visible: false, Masked: false},
		{Role: "store_s1_staff", TableName: "customers", Field: "phone", Visible: true, Masked: true},
		{Role: "store_s1_staff", TableName: "customers", Field: "email", Visible: true, Masked: true},
	}
	return db.Create(&grants).Error
}
