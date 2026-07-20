# MCP 数据查询服务

基于 Go + GORM + MySQL/SQLite 的 MCP 服务，作为数据网关提供结构化查询、文件读写、联网搜索等能力。

## 核心能力

- 结构化查询：参数化 `query_table`，自动列名白名单校验
- 原生 SQL：可配置开关，只读检测 + 危险关键字拦截
- 多租户行级隔离：`tenant_id / region_id / store_id` 自动过滤
- 文件沙箱：读/写/追加/列目录/删除，限定在 `work_dir` 内
- 联网搜索：DuckDuckGo / Bing 切换
- 数据脱敏：列级规则，自动脱敏手机号/邮箱/身份证等
- 审计日志：每次工具调用写入 `audit_logs`
- 可选 HTTP 模式：适配标准 MCP 客户端（streamable-http / SSE）

## 目录结构

```
cmd/server/main.go          入口：装配各层并启动
config/                     配置层
internal/
  model/                    领域模型（业务表/审计）
  security/                 安全校验（SQL/标识符白名单）
  repository/               数据访问层
  service/                  业务服务层（查询编排、脱敏）
  handler/                  MCP 工具定义与处理器
```

## 运行

```bash
go run ./cmd/server

# MySQL（默认 SQLite）
DB_DIALECT=mysql DB_DSN='user:pass@tcp(127.0.0.1:3306)/mcp?...' go run ./cmd/server
```

## HTTP 模式

```bash
TRANSPORT=http HTTP_ADDR=:8081 go run ./cmd/server
```

| 字段 | 环境变量 | 说明 |
|------|----------|------|
| `transport` | `TRANSPORT` | `stdio` / `http` / `both` |
| `http_addr` | `HTTP_ADDR` | 监听地址，默认 `:8081` |

HTTP 端点：
- `POST /mcp` — streamable-http（推荐）
- `GET /sse` + `POST /messages` — 旧版 SSE 传输

## 工具列表

1. `query_table(table, fields?, filters?, order?, limit?, offset?)` → 查询（自动行级隔离 + 脱敏）
2. `run_sql(sql)` → 原生 SQL（仅限 super_admin，只读检测）
3. `describe_table(table)` → 表结构
4. `read_file(path, max_bytes?)` / `write_file(path, content)` / `append_file(path, content)` / `list_dir(path?)` / `make_dir(path)` / `delete_file(path)` / `read_dir_tree(path?)`
5. `web_search(query)` / `web_fetch(url)` — 联网搜索
6. `get_weather(city)` — 天气查询

## 配置

| 字段 | 环境变量 | 说明 |
|------|----------|------|
| `db_dialect` | `DB_DIALECT` | `sqlite` / `mysql` |
| `db_dsn` | `DB_DSN` | 连接串 |
| `seed_demo` | `SEED_DEMO` | 是否写入演示数据 |
| `work_dir` | `WORK_DIR` | 文件沙箱根目录 |
| `sandbox_enabled` | `SANDBOX_ENABLED` | 是否启用沙箱（默认 true） |
| `search_provider` | `SEARCH_PROVIDER` | `duckduckgo` / `bing` / `auto` |
