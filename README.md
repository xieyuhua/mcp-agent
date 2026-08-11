# MCP 数据查询服务

基于 Go + GORM + MySQL/SQLite 的 MCP 服务，作为**数据网关**为大模型 / Agent 提供结构化查询、文件读写、联网搜索等能力。

核心特色：内置 **gosqlx SQL 引擎**实现按角色的**数据权限隔离**，并配套独立的 **Vue 管理后台**用于可视化配置权限与表结构元数据。

## 核心能力

- **数据权限隔离**：agent 传入 `origin_role`，自动注入后台配置的行级 `where` 条件，支持**表别名、多表 JOIN、子查询**
- **元数据驱动**：大模型不再直连数据库探测 schema，改为读取后台配置的**表注释 / 字段注释 / 表关联关系**
- **字段级权限**：按角色控制字段可见 / 脱敏
- **SQL 安全校验**：仅允许 SELECT，危险关键字拦截，表白名单校验
- **可视化管理后台**：Vue3 独立项目，内嵌进二进制，开箱即用
- 文件沙箱：读/写/追加/列目录/删除，限定在 `work_dir` 内
- 系统信息：跨平台（Windows / Linux）目录识别、磁盘空间、环境变量
- 联网搜索：DuckDuckGo / Bing 切换
- 审计日志：每次工具调用写入 `audit_logs`
- 可选 HTTP 模式：适配标准 MCP 客户端（streamable-http / SSE）
- **AI 一键完善业务名称**：管理后台「表结构配置」每张表提供「AI 完善」按钮，由大模型依据物理表名/字段信息生成**业务名称与表注释**建议，确认后填充保存，减少人工补注释成本（需在 config.json 配置 `llm`）

## 文档索引

| 文档 | 内容 |
|------|------|
| [docs/permission.md](docs/permission.md) | 权限体系设计：模型、gosqlx 注入原理、别名/JOIN/子查询处理 |
| [docs/admin.md](docs/admin.md) | 管理后台使用说明：页面功能、配置流程、API 接口 |
| [docs/mcp-tools.md](docs/mcp-tools.md) | MCP 工具清单与调用示例 |

## 目录结构

```
cmd/server/main.go          入口：装配各层并启动
config/                     配置层
internal/
  model/                    领域模型（业务表/审计/权限配置）
  gosqlx/                   自研 SQL 解析与权限注入引擎
  security/                 安全校验（SQL/标识符白名单）
  repository/               数据访问层
  service/                  业务服务层（查询编排、权限、脱敏）
  handler/                  MCP 工具定义 + 管理后台 API
web/admin/                  Vue3 管理后台（独立项目，构建产物 embed 进二进制）
docs/                       项目文档
```

## 快速开始

### 1. 构建管理后台前端

```bash
cd web/admin
npm install
npm run build      # 产物输出到 web/admin/dist，由 Go embed 内嵌
```

> 未构建也能正常编译启动，仅 `/admin` 页面会提示需先构建。

### 2. 启动服务

```bash
go run ./cmd/server

# MySQL（默认 SQLite）
DB_DIALECT=mysql DB_DSN='user:pass@tcp(127.0.0.1:3306)/mcp?...' go run ./cmd/server
```

### 3. 打开管理后台

启用 HTTP 模式后访问 <http://127.0.0.1:8081/admin>

```bash
TRANSPORT=http HTTP_ADDR=:8081 go run ./cmd/server
```

首次启动（数据库无账号时）会自动创建管理员 `admin` 并**在日志中打印初始密码**，
登录后请立即在右上角修改密码。详见 [docs/admin.md](docs/admin.md) 的「首次安装」与「鉴权说明」。

### 前端开发模式（热更新）

```bash
cd web/admin
npm run dev        # http://127.0.0.1:5174，/api 自动代理到 Go 服务
```

## HTTP 模式

| 字段 | 环境变量 | 说明 |
|------|----------|------|
| `transport` | `TRANSPORT` | `stdio` / `http` / `both` |
| `http_addr` | `HTTP_ADDR` | 监听地址，默认 `:8081` |

HTTP 端点：
- `POST /mcp` — streamable-http（推荐）
- `GET /sse` + `POST /messages` — 旧版 SSE 传输
- `GET /admin` — Vue 管理后台
- `/api/admin/*` — 管理后台 REST API

## 工具列表

数据类工具均支持 `origin_role` 参数驱动权限，详见 [docs/mcp-tools.md](docs/mcp-tools.md)。

1. `list_schema(origin_role?)` → **推荐首选**，列出全部已开放表的元数据
2. `describe_table(origin_role?, table)` → 单表结构（后台配置的注释与关联）
3. `query_table(origin_role?, table, fields?, filters?, order?, limit?, offset?)` → 结构化查询
4. `run_sql(origin_role?, sql)` → 原生只读 SQL，自动注入权限条件
5. `read_file` / `write_file` / `append_file` / `list_dir` / `make_dir` / `delete_file` / `read_dir_tree`
6. `web_search(query, limit?)` / `web_fetch(url, max_chars?)`
7. `query_weather(location)`
8. `system_info()` / `system_dirs()` / `read_system_dir(path?, max_entries?)` / `get_env(name?)` / `disk_usage(path?)`

## 配置

| 字段 | 环境变量 | 说明 |
|------|----------|------|
| `db_dialect` | `DB_DIALECT` | `sqlite` / `mysql` |
| `db_dsn` | `DB_DSN` | 连接串 |
| `seed_demo` | `SEED_DEMO` | 是否写入演示数据（含权限示例配置） |
| `work_dir` | `WORK_DIR` | 文件沙箱根目录 |
| `sandbox_enabled` | `SANDBOX_ENABLED` | 是否启用沙箱（默认 true） |
| `search_provider` | `SEARCH_PROVIDER` | `duckduckgo` / `bing` / `auto` |
| `llm.provider` | `LLM_BASE_URL` 等 | AI 一键完善所用大模型（OpenAI 兼容），见下 |

### AI 一键完善大模型配置（llm）

「AI 完善」按钮通过 OpenAI 兼容的 `chat/completions` 接口生成业务名称/注释。在 `config.json` 中增加 `llm` 段：

```json
{
  "llm": {
    "provider": "ollama",
    "base_url": "http://localhost:11434",
    "api_key": "",
    "model": "qwen2.5:14b",
    "temperature": 0.3,
    "max_tokens": 512
  }
}
```

- `provider`：`ollama` / `openai`(两者均使用 chat/completions 风格接口)。
- `base_url`：服务地址(如 Ollama 默认 `http://localhost:11434`，OpenAI 兼容服务填对应地址)。
- `api_key`：仅 OpenAI 兼容服务需要。
- 也可用环境变量 `LLM_BASE_URL` / `LLM_API_KEY` / `LLM_MODEL` 覆盖。
- 未配置时点击「AI 完善」会提示「未配置大模型」；审计/查询能力不依赖此项。

## 测试

```bash
go test ./...
```
