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
- **两种传输方式**：默认 `stdio`（子进程，供本地 Agent 内置对接）；也可 `http` / `both` 以 HTTP 暴露 MCP，**对接任意标准 MCP 客户端 / 其他 Agent 智能体**。
- **HTTP 双协议**：HTTP 模式同时支持 `streamable-http`（默认，推荐）与旧版 `sse` 两种标准 MCP 传输。
- **权限后台 Web 服务**：内置 HTTP 管理后台（REST API + 页面），平台运营可可视化配置角色策略与脱敏规则。
- **Web 资源可打包**：管理页面随 Go 二进制 `embed` 内嵌（单文件分发），也可通过 `web_dir` 指定外部目录热更新前端。
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
  transport/                stdio 与 http 传输层
  handler/                  工具定义与处理器（桥接 MCP ↔ 业务层）
  admin/                    权限后台 REST API（登录/策略/脱敏）
  web/                      Web 资源 embed 打包与静态服务（含 assets/）
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
5. 文件 / 目录读写（均限定在 `work_dir` 沙箱内，token 鉴权）：
   - `read_file(token, path, max_bytes?)` → 读取文本文件内容
   - `write_file(token, path, content)` → 覆盖写入（父目录自动创建）
   - `append_file(token, path, content)` → 追加写入
   - `list_dir(token, path?)` → 列出目录内容
   - `make_dir(token, path)` → 创建目录（含多级父目录）
   - `delete_file(token, path)` → 删除文件（不删目录）
   - `read_dir_tree(token, path?)` → 递归目录树（最多两层）

## 文件读写配置

- 通过配置项 `work_dir`（或环境变量 `WORK_DIR`）指定文件工具的根目录（沙箱）。
- 留空时默认进程工作目录。
- 所有文件工具路径都相对于 `work_dir`，并强制拦截 `..` 越界访问，禁止读写沙箱之外的任何文件。

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

## HTTP 模式（对接其他 Agent / 标准 MCP 客户端）

除默认 `stdio` 外，服务可经 HTTP 暴露 MCP，从而被任意标准 MCP 客户端（如其他 Agent 智能体、Claude Desktop 的 HTTP 模式、自研 Agent 的 remote 模式）对接。

### 启动方式

```bash
# 仅 HTTP
TRANSPORT=http HTTP_ADDR=:8081 go run ./cmd/server

# 同时支持 stdio（内置子进程）与 HTTP
TRANSPORT=both HTTP_ADDR=:8081 go run ./cmd/server
```

配置项（`config.json` 或环境变量）：

| 字段 | 环境变量 | 说明 |
|------|----------|------|
| `transport` | `TRANSPORT` | `stdio`（默认）\| `http` \| `both` |
| `http_addr` | `HTTP_ADDR` | HTTP 监听地址，默认 `:8081` |
| `web_dir`   | `WEB_DIR`   | 外部 Web 目录；为空则用二进制内嵌页面 |

HTTP 暴露的端点：

- `POST /mcp` —— **streamable-http**（默认，推荐）：请求可为 JSON 或 SSE 流，自动处理 `Mcp-Session-Id` 会话头。
- `GET /sse` + `POST /messages` —— **旧版 sse** 传输：GET 建立接收流，POST 发送请求。
- `/api/admin/*` —— 权限后台 REST API（见下）。
- `/` —— 权限后台管理页面（内嵌或外部目录）。

### 对接示例（本项目 data-analysis-agent 的 remote 模式）

```json
// data-analysis-agent/config.json
"mcp": {
  "mode": "remote",
  "base_url": "http://127.0.0.1:8081/mcp",
  "transport": "streamable-http"
}
```

> 远程对接时工具清单直接取自本服务的 `tools/list`（含 `auth_login` 等），由对接方 Agent 自行编排调用；`auth_login` 获取 token 后按工具要求携带即可。

## 权限后台 Web 服务

平台运营可通过浏览器可视化配置权限，无需在 MCP 客户端里手敲工具调用。

1. 启动 HTTP 模式（`TRANSPORT=http` 或 `both`）。
2. 浏览器打开 `http://<http_addr>/`，用 `admin / admin123` 登录。
3. 在「角色权限策略」「数据脱敏规则」两个页签中查看、新增、删除配置，**即时生效**。

### 后台 REST API（仅 `super_admin`）

| 方法 + 路径 | 说明 |
|-------------|------|
| `POST /api/admin/login` | 登录，返回 `token`（复用 MCP 令牌） |
| `GET  /api/admin/policies?tenant_id=` | 列出角色策略 |
| `POST /api/admin/policies` | 新增/修改角色策略 |
| `DELETE /api/admin/policies?tenant_id=&role=` | 删除角色策略 |
| `GET  /api/admin/mask-rules?tenant_id=` | 列出脱敏规则 |
| `POST /api/admin/mask-rules` | 新增/修改脱敏规则 |
| `DELETE /api/admin/mask-rules?tenant_id=&table=&column=` | 删除脱敏规则 |

调用示例：

```bash
TOKEN=$(curl -s -X POST http://localhost:8081/api/admin/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}' | jq -r .token)

curl http://localhost:8081/api/admin/policies \
  -H "Authorization: Bearer $TOKEN"
```

### Web 页面打包

管理页面（`index.html` / `app.js` / `style.css`）通过 `//go:embed all:assets` **编译进二进制**，因此单文件 `main.exe` 即可直接提供 Web 服务，无需额外静态资源目录。

如需分离部署或热更新前端，设置 `web_dir` 指向外部目录即可优先从该目录加载：

```bash
WEB_DIR=./web-dist TRANSPORT=http HTTP_ADDR=:8081 go run ./cmd/server
```

两种方式均支持单页应用（SPA）路由回退。
