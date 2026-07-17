# MCP 数据查询服务（多租户 SaaS）

基于 **Golang + GORM + MySQL/SQLite** 的 MCP 服务，面向公司内部数据查询与数据分析场景，提供多层业务分层架构。

## 核心能力

- **多租户 SaaS 鉴权**：HMAC 签名令牌，承载 `tenant_id / role / region_id / store_id`。
- **细粒度权限**：角色（平台运营 / 区域负责人 / 门店店长 / 岗位 / 数据分析师）决定可见数据范围与可访问表。
- **数据隔离（行级）**：`query_table` 自动叠加 `tenant_id / region_id / store_id` 过滤条件，越权不可见。
- **SQL 注入防护**：结构化查询全程参数化；原生 SQL 仅平台运营可用，且强制 `SELECT` 并拦截危险关键字。
- **数据脱敏**：按表+列配置规则，对手机号/邮箱/身份证/姓名/密码等自动脱敏。
- **审计模块**：每次工具调用（表、查询、影响行数、操作人）落库 `audit_logs`。
- **权限可视化设置管理**：角色策略（数据范围/表白名单/原SQL）与脱敏规则均持久化到数据库，提供 `perm_view` / `perm_set` / `mask_view` / `mask_set` 等工具，由平台运营在 MCP 客户端中可视化查看与修改，**即时生效无需重启**。
- **双数据库**：通过配置切换 MySQL 或 SQLite（SQLite 使用纯 Go 驱动，免 CGO）。

## 目录结构（多层业务分层）

```
cmd/server/main.go          入口：装配各层并启动
config/                     配置层
internal/
  model/                    领域模型（租户/用户/业务表/审计/权限策略/脱敏规则）
  tenant/                   多租户上下文（贯穿各层）
  auth/                     权限策略（角色→数据范围、表白名单，Resolver 从DB读取+缓存）
  mask/                     数据脱敏（Resolver 从DB读取+缓存）
  security/                 安全校验（SQL/标识符）
  repository/               数据访问层（GORM：隔离查询、原生SQL、种子、权限CRUD）
  service/                  业务服务层（鉴权、审计、查询编排、权限配置）
  mcp/                      MCP 协议层（JSON-RPC 分发）
  transport/                stdio 传输层
  handler/                  工具定义与处理器（桥接 MCP ↔ 业务层）
```

## 权限可视化设置管理

权限不再硬编码，而是以数据库配置表驱动，可运行时修改并立即生效：

- `permission_policies`：角色级策略 —— 数据范围（all/tenant/region/store）、可访问表白名单、是否允许原SQL。
  - 支持「平台全局默认」（tenant_id 为空）+「租户级覆盖」（tenant_id 具体值）两级，租户级优先。
- `mask_rules`：列级脱敏规则 —— 表.列 → 脱敏类型（phone/email/idcard/name/money/secret），可启用/禁用。
  - 同样支持平台默认 + 租户级覆盖；租户级显式禁用可关闭平台默认脱敏。

### 管理工具（仅 `super_admin` 平台运营可用）

| 工具 | 作用 |
|------|------|
| `perm_view`   | 查看当前租户（含平台默认）的全部角色权限策略 |
| `perm_set`    | 设置/修改角色策略（data_scope、allowed_tables、can_raw_sql），即时生效 |
| `perm_delete` | 删除租户级策略（回退到平台默认） |
| `mask_view`   | 查看当前租户（含平台默认）的全部脱敏规则 |
| `mask_set`    | 设置/修改列级脱敏规则（mask_type、enabled），即时生效 |
| `mask_delete` | 删除租户级脱敏规则 |

所有管理操作都会写入 `audit_logs` 审计日志。

### 配置示例

```jsonc
// 将门店店长（t1 租户）的数据范围从「仅本门店」放宽到「本租户」
{ "tool": "perm_set", "arguments": {
  "token": "<admin_token>", "tenant_id": "t1", "role": "store_manager",
  "data_scope": "tenant", "allowed_tables": ["customers", "orders"]
}}

// 关闭 t1 租户 customers.phone 的脱敏
{ "tool": "mask_set", "arguments": {
  "token": "<admin_token>", "tenant_id": "t1",
  "table": "customers", "column": "phone", "mask_type": "phone", "enabled": false
}}
```

> 修改后内存缓存立即刷新（Resolver.Refresh），无需重启服务；下次查询即按新策略执行隔离与脱敏。


## 运行

```bash
# SQLite（默认，免依赖）
go run ./cmd/server

# MySQL
DB_DIALECT=mysql DB_DSN='user:pass@tcp(127.0.0.1:3306)/mcp?charset=utf8mb4&parseTime=true' go run ./cmd/server

# 依赖安装
go mod tidy
```

## 演示账号（seed 自动写入）

| 用户名    | 密码       | 角色           | 数据范围            |
|-----------|------------|----------------|---------------------|
| admin     | admin123   | super_admin    | 全部租户            |
| region1   | region123  | region_manager | 租户 t1 + 区域 r1   |
| store1    | store123   | store_manager  | 租户 t1 + 门店 s1   |
| staff1    | staff123   | staff          | 租户 t1 + 门店 s1   |
| analyst1  | analyst123 | analyst        | 租户 t1（只读）     |

## 工具列表

1. `auth_login(username, password)` → 返回 `token`
2. `query_table(token, table, fields?, filters?, order?, limit?, offset?)` → 隔离+脱敏结果
3. `run_sql(token, sql)` → 仅 super_admin，只读校验
4. `describe_table(token, table)` → 表结构

## 接入 MCP 客户端（示例：Claude Desktop）

```json
{
  "mcpServers": {
    "data-server": {
      "command": "go",
      "args": ["run", "/path/to/mcp-data-server/cmd/server"]
    }
  }
}
```

## 安全要点说明

- 非平台角色**无法执行原生 SQL**，只能走 `query_table`，从根本上避免绕过行级隔离。
- 结构化查询的过滤值全部走 GORM 参数绑定，列名经正则白名单校验。
- 敏感字段脱敏在服务层统一处理，数据库返回后、结果输出前生效。
- 所有访问均留痕至 `audit_logs`，便于合规审计。
