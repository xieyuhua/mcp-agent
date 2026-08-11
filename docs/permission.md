# 数据权限体系设计

本文档说明 MCP 数据服务如何基于 `origin_role` 实现**行级数据隔离**与**字段级权限**，以及自研 `gosqlx` 引擎的 SQL 改写原理。

## 1. 整体流程

```
Agent / 大模型
   │  调用 MCP 工具，传入 origin_role
   ▼
ToolHandler ──► PermissionService ──► gosqlx 引擎
   │                  │                   │
   │                  │  1. Validate      │ 只读校验 + 表白名单
   │                  │  2. Parse         │ 识别表、别名、JOIN、子查询作用域
   │                  │  3. InjectWhere   │ 按作用域注入行级条件
   │                  ▼
   │             最终 SQL ──► Repository ──► 数据库
   ▼
结果集 ──► 字段可见性过滤 + 脱敏 ──► 返回给大模型
```

关键点：**权限在服务端强制执行**，大模型无法绕过。即使它生成了越权 SQL，也会被校验拦截或被自动加上限制条件。

## 2. 配置模型

全部通过 Vue 管理后台维护，对应 5 张配置表：

| 模型 | 作用 |
|------|------|
| `Role` | 角色字典。`code` 即 agent 传入的 `origin_role` |
| `TableConfig` | 表元数据：物理表名、业务名、**表注释**、是否对 agent 开放 |
| `FieldConfig` | 字段元数据：字段名、业务名、类型、**字段注释**、是否敏感 |
| `TableRelation` | 表关联关系：左右表及关联列、JOIN 类型，供大模型理解如何关联 |
| `RolePolicy` | **行级权限**：某角色对某表的 `where` 条件模板 |
| `RoleFieldGrant` | **字段权限**：某角色对某表某字段的可见性 / 脱敏 |

### 超级管理员

常量 `service.SuperRole = "super_admin"`，该角色**不受**行级与字段权限限制，也不做表白名单校验。

## 3. 行级权限：`{alias}` 占位符

`RolePolicy.Condition` 是一段 `where` 模板，使用 `{alias}` 指代当前表：

```sql
{alias}.tenant_id = 't1'
{alias}.region_id IN ('r1','r2')
{alias}.status = 1 AND {alias}.deleted_at IS NULL
```

引擎在解析 SQL 后，把 `{alias}` 替换为该表在语句中的**真实别名**；若表没有别名，则替换为表名本身。

同一角色对同一张表配置多条策略时，多个条件之间以 `AND` 合并。

## 4. gosqlx 引擎

`internal/gosqlx` 为零外部依赖的自研实现，包含四个部分：

| 文件 | 职责 |
|------|------|
| `token.go` | 词法分析，切分标识符/关键字/字符串/符号，并检测注释 |
| `parser.go` | 解析 SELECT，提取表名与别名，划分 JOIN 与子查询的作用域及 WHERE 边界 |
| `rewriter.go` | 按作用域渲染 `{alias}` 并注入行级条件 |
| `validator.go` | 只读校验、危险关键字拦截、表白名单校验 |

### 4.1 SQL 安全校验

`Validate` 依次执行：

1. **禁止注释** —— 防止用注释截断语句绕过校验
2. **危险关键字拦截** —— `insert / update / delete / drop / alter / create / truncate / grant / revoke / exec / call / pragma / load_file / outfile / information_schema` 等。检测基于 **token 级**匹配，因此 `created_at` 这类字段名不会被误伤
3. **仅允许 SELECT**
4. **表白名单** —— 只能访问后台已启用的表，越权表直接报错

### 4.2 别名适配

引擎会正确处理各种别名写法：

```sql
-- 无别名：{alias} → orders
SELECT * FROM orders WHERE amount > 100
-- 注入后
SELECT * FROM orders WHERE (orders.tenant_id = 't1') AND (amount > 100)

-- 有别名：{alias} → o
SELECT * FROM orders o WHERE o.amount > 100
-- 注入后
SELECT * FROM orders o WHERE (o.tenant_id = 't1') AND (o.amount > 100)

-- AS 别名同样支持
SELECT * FROM orders AS o
```

### 4.3 多表 JOIN

**每一张**参与查询的表都会各自注入其对应的条件：

```sql
-- 原始
SELECT o.id, c.name
FROM orders o
JOIN customers c ON o.customer_id = c.id
WHERE o.amount > 100

-- 注入后（orders 与 customers 各自的策略都生效）
SELECT o.id, c.name
FROM orders o
JOIN customers c ON o.customer_id = c.id
WHERE (o.tenant_id = 't1') AND (c.tenant_id = 't1') AND (o.amount > 100)
```

### 4.4 子查询

子查询拥有**独立作用域**，条件注入到子查询自己的 WHERE 中，不会污染外层：

```sql
-- 原始
SELECT * FROM orders
WHERE customer_id IN (SELECT id FROM customers WHERE level = 1)

-- 注入后
SELECT * FROM orders
WHERE (orders.tenant_id = 't1')
  AND (customer_id IN (
        SELECT id FROM customers WHERE (customers.tenant_id = 't1') AND (level = 1)
      ))
```

若原语句某个作用域没有 WHERE，引擎会自动补上。

## 5. 字段级权限

在结果集返回前处理，规则优先级：

1. 若该表**未配置**任何 `RoleFieldGrant` → 全部字段可见，其中 `FieldConfig.Sensitive = true` 的字段按脱敏规则处理
2. 若已配置：
   - `Visible = false` → 字段从结果中**移除**
   - `Masked = true` → 字段值**脱敏**后返回

脱敏针对手机号、邮箱、身份证等常见类型。

> **实现注意**：这些布尔字段**不能**使用 GORM 的 `default:true` 标签。GORM 在 `Create` 时会忽略 Go 零值 `false`，导致数据库默认值把 `false` 覆盖成 `true`，出现「配置了不可见却依然可见」的问题。因此模型中统一去掉了布尔列的 default 标签。

## 6. 元数据驱动大模型

大模型**不直连数据库**探测 schema，而是通过 `list_schema` / `describe_table` 读取后台配置的元数据：

- 表注释说明业务含义
- 字段注释说明取值范围与枚举
- 表关联关系告诉模型如何 JOIN

这样做的好处：

1. **可控** —— 只暴露想让模型看到的表和字段
2. **语义更准** —— 业务注释比裸列名更利于模型生成正确 SQL
3. **权限一致** —— 返回的 schema 已按角色过滤掉不可见字段，模型不会尝试查询无权字段

## 7. 调试

管理后台的 **SQL 权限调试**页面可以模拟任意角色提交 SQL，查看：

- 校验是否通过、被拦截的原因
- 注入权限后的**最终 SQL**
- 识别到的表与别名、各自命中的条件
- 将被脱敏 / 隐藏的字段

该页面仅做解析改写预览，**不会真正执行**查询。
