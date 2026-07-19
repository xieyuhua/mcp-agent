# 数据分析助手 Agent（本地大模型 + MCP + MySQL）

一个用 Golang 编写的数据分析助手。它把「自然语言 → 本地大模型 → 生成 SQL → MCP 权限处理 → 查 MySQL → 大模型分析 → 输出给用户」整条链路打通。

```
用户自然语言
    │
    ▼
┌──────────────────────────┐
│  数据分析助手 Agent (Go)   │
│  · ReAct 推理循环          │
│  · 本地大模型对接           │
└──────────┬───────────────┘
           │ ① 生成 SQL / 选择工具
           ▼
┌──────────────────────────┐
│   本地大模型 (Ollama/OpenAI)│  ← 自然语言理解与 SQL 生成、结果分析
└──────────┬───────────────┘
           │ ② 工具调用 (native function calling)
           ▼
┌──────────────────────────┐
│  mcp-data-server (MCP 后端) │  ← 权限隔离 / 数据脱敏 / 危险SQL拦截 / 审计
└──────────┬───────────────┘
           │ ③ 执行查询
           ▼
        MySQL / SQLite
           │ ④ 返回数据
           ▼
   大模型分析 → 输出结论给用户
```

## 核心特性

- **本地大模型对接**：基于标准 `chat` 接口，兼容 **Ollama**（`/api/chat`）与 **OpenAI** 风格（`/v1/chat/completions`），支持原生 function calling。
- **MCP 对接（本地 / 远程 / 多路并存）**：Agent 作为 MCP **客户端**，支持：
  - **本地内置**（`local_enabled=true`）：自动拉起 `mcp-data-server` 子进程（stdio JSON-RPC），复用其「权限隔离 + 数据脱敏 + 危险 SQL 拦截 + 审计」能力。
  - **远程对接**（`remote_enabled=true`）：对接任意远程 MCP 服务，支持 `streamable-http`（默认，推荐）与旧版 `sse` 两种传输协议，可配置鉴权头。
  - **多路并存与聚合**：本地、主远程、以及 `extra` 列表中的多个额外远程 MCP 可同时开启，工具按名称自动路由聚合；本地 MCP 可独立开关，不影响远程。
- **Tools 连接 MySQL**：通过 MCP 后端的 `query_table` / `run_sql` 工具真正落到 MySQL（或 SQLite 演示库）执行查询。
- **ReAct 编排**：模型可多轮调用工具（先看表结构 → 生成 SQL → 取数 → 分析），直到给出最终结论。
- **技能（Skill）热加载**：内置多套企业数据分析技能（销售/RFM/留存/漏斗/ABC/区域门店/异常/流失等），修改或新增技能文件后**无需重启**即可生效（后台一键重载或改配置自动重载）。
- **配置热更新（免重启）**：MCP 开关、远程地址、`extra` 列表、技能目录、LLM 参数、日志开关、结果截断阈值等改动，保存到后台后由 `ApplyConfig` 自动重建对应连接或生效，**无需重启进程**。
- **零外部依赖**：仅用 Go 标准库实现 HTTP 与 MCP 通信，构建无需联网拉包。

## 目录结构

```
data-analysis-agent/
├── main.go                 # 入口：REPL / 单次提问 / -serve 启动 HTTP
├── config.json             # 运行配置（首次作为数据库种子）
├── config/                 # 配置加载
├── mcpclient/              # MCP 客户端（传输层抽象：stdio 本地 / http 远程）
├── llm/                    # 本地大模型客户端（Ollama / OpenAI 兼容）
├── agent/                  # Agent 编排（ReAct 循环 + 工具定义 + 权限处理 + 技能热加载）
├── skill/                  # 技能加载与解析（frontmatter + 工作流正文）
├── skills/                 # 内置企业数据分析技能（.md，运行时热加载）
├── server/                 # HTTP 服务（/api/ask、/api/models、聊天页、后台）
├── internal/
│   ├── confdb/             # 配置持久化到 SQLite（数据库为准 + 热更新）
│   ├── admin/              # 后台管理（配置 CRUD + 页面，/admin）
│   └── webui/              # 聊天前端页面（自包含单文件，/ 与 /ui）
└── web/                    # 可选 Vue 前端（npm 构建后挂载于 /app/）
```

## 编译

```powershell
# 1) 先编译 mcp-data-server（本 Agent 的子进程）
cd ../mcp-data-server
go build -o main.exe ./cmd/server

# 2) 编译数据分析助手
cd ../data-analysis-agent
go build -o data-analysis-agent.exe .
```

## 配置 config.json

```json
{
  "llm": {
    "provider": "ollama",                 // ollama | openai
    "base_url": "http://localhost:11434",  // 本地大模型地址
    "model": "qwen2.5:14b",               // 本地模型名
    "temperature": 0.2,
    "max_tokens": 2048
  },
  "mcp": {
    "mode": "local",                      // 兼容旧配置：local | remote（新配置用下方两个开关）
    "local_enabled": true,                // 是否启用本地内置 mcp-data-server（默认 true）
    "remote_enabled": false,              // 是否启用远程 MCP 服务（需配置 base_url）

    // --- 本地（内置 mcp-data-server 子进程）相关 ---
    "server_path": "../mcp-data-server/main.exe",
    "db_dialect": "sqlite",               // sqlite(演示) | mysql(真实分析)
    "db_dsn": "./data.db",                // mysql: user:pass@tcp(127.0.0.1:3306)/db?charset=utf8mb4&parseTime=true
    "username": "admin",                  // 登录账号（Agent 自动登录获取 token）
    "password": "admin123",
    "mask_enabled": true,                 // 是否启用脱敏
    "seed_demo": true,                    // 是否写入演示数据
    "work_dir": "workdir",                // 文件工具沙箱根目录（透传 WORK_DIR 给 mcp 子进程）
    "sandbox_enabled": true,              // 文件工具沙箱开关（true 限 work_dir 内）

    // --- 远程 MCP 服务相关 ---
    "base_url": "http://127.0.0.1:9000/mcp", // 远程 MCP 地址
    "transport": "streamable-http",       // streamable-http(默认) | sse
    "api_key": "",                        // 可选 Bearer 鉴权
    "headers": {},                       // 额外请求头

    // --- 额外远程 MCP 列表（可配多个，与主 MCP 并存聚合）---
    "extra": [
      {"name": "天气服务", "base_url": "http://host2:8000/mcp", "transport": "streamable-http"},
      {"name": "日历服务", "base_url": "http://host3:8000/mcp", "transport": "sse", "api_key": "xxx"}
    ]
  },
  "agent": {
    "max_steps": 8,                       // ReAct 最大步数
    "use_native_tools": true,             // 是否使用原生 function calling
    "max_result_rows": 200,               // 单次工具返回最多保留行数（防上下文膨胀；0=不限制）
    "max_result_chars": 0,                // 工具返回字符截断上限：0=不截断（默认），>0 时超长截断
    "log_preview_chars": 0                // 工具返回写入日志前的预览字符上限：0=不截断（默认，记录完整结果）
  },
  "log": {
    "save_to_file": false,                // 是否把每个环节（HTTP/LLM/MCP/工具）的日志带时间戳保存到文件
    "dir": "logs"                         // 日志文件目录（默认 logs），文件按天滚动为 agent-YYYY-MM-DD.log
  }
}
```

### 配置项速查

| 分组 | 字段 | 默认 | 说明 |
|---|---|---|---|
| `llm` | `provider` | `ollama` | `ollama` / `openai` |
| | `base_url` | `http://localhost:11434` | 大模型服务地址 |
| | `model` | `qwen2.5:14b` | 模型名 |
| | `temperature` / `max_tokens` | `0.2` / `2048` | 生成参数 |
| `mcp` | `local_enabled` / `remote_enabled` | `true` / `false` | 本地 / 远程开关（互不冲突） |
| | `server_path` | `../mcp-data-server/main.exe` | 本地子进程路径 |
| | `db_dialect` / `db_dsn` | `sqlite` / `./data.db` | 后端库类型与连接串 |
| | `username` / `password` | `admin` / `admin123` | MCP 登录凭据 |
| | `mask_enabled` / `seed_demo` | `true` / `true` | 脱敏 / 演示数据 |
| | `work_dir` / `sandbox_enabled` | `workdir` / `true` | 文件沙箱根目录 / 开关 |
| | `base_url` / `transport` | — / `streamable-http` | 远程地址 / 传输协议 |
| | `api_key` / `headers` | 空 | 远程鉴权 |
| | `extra` | 空 | 额外远程 MCP 列表（多路并存） |
| `agent` | `max_steps` | `8` | ReAct 最大步数 |
| | `use_native_tools` | `true` | 原生工具调用 |
| | `max_result_rows` | `200` | 工具返回最大行数（0=不限制） |
| | `max_result_chars` | `0` | 字符截断上限（0=不截断） |
| | `log_preview_chars` | `0` | 日志预览字符上限（0=不截断） |
| `log` | `save_to_file` / `dir` | `false` / `logs` | 日志落盘开关 / 目录 |

> 所有配置项均可在后台（`/admin`）修改并**热更新**；未显式配置时按上表默认值生效（截断类项默认 `0` = 不截断）。

### MCP 部署形态（本地 / 远程 / 多路并存）

本地与远程**互不冲突**，由 `local_enabled` / `remote_enabled` 两个独立开关控制，可任意组合：

| `local_enabled` | `remote_enabled` | 效果 |
|---|---|---|
| `true` | `false` | 仅本地内置 MCP（默认） |
| `false` | `true` | 仅远程 MCP（远程作为主 MCP） |
| `true` | `true` | 本地为主 MCP + 远程聚合为额外 MCP（多路并存） |
| `false` | `false` | 按旧 `mode` 决定（local=本地 / remote=远程） |

- **多路并存时的工具路由**：所有 MCP 的工具名汇总暴露给大模型，调用时按工具名自动路由到对应客户端；出现同名工具时以先注册者为准，后到的跳过并告警（不会互相覆盖）。
- **本地可独立关闭**：把 `local_enabled` 设为 `false` 即可只跑远程，不影响任何远程配置。
- **多个远程 MCP**：除主远程 `base_url` 外，还可在 `extra` 列表中追加任意多个额外远程服务，全部聚合。

**仅远程、关闭本地的配置示例：**

```json
"mcp": {
  "local_enabled": false,
  "remote_enabled": true,
  "base_url": "http://192.168.1.10:9000/mcp",
  "transport": "streamable-http",
  "api_key": "可选Bearer令牌",
  "headers": { "X-Custom": "value" }
}
```

**本地 + 主远程 + 多个额外远程（全部并存）的配置示例：**

```json
"mcp": {
  "local_enabled": true,
  "remote_enabled": true,
  "server_path": "../mcp-data-server/main.exe",
  "db_dialect": "sqlite",
  "db_dsn": "./data.db",
  "username": "admin",
  "password": "admin123",
  "base_url": "http://192.168.1.10:9000/mcp",
  "transport": "streamable-http",
  "extra": [
    {"name": "天气服务", "base_url": "http://host2:8000/mcp", "transport": "streamable-http"},
    {"name": "日历服务", "base_url": "http://host3:8000/mcp", "transport": "sse", "api_key": "xxx"}
  ]
}
```

**远程传输方式说明：**
- `transport=streamable-http`（默认，推荐）：通过 `POST` 发送 JSON-RPC，服务端可在响应中以 `text/event-stream`（SSE 流）或普通 JSON 返回结果，自动处理 `Mcp-Session-Id` 会话头。
- `transport=sse`（旧版协议）：先 `GET /sse` 建立接收流，再 `POST /messages` 发送请求，响应经 SSE 流异步回传并按 id 匹配。

对接远程服务时：不再拉起本地子进程，也不做 `auth_login` / token 注入（除非远程服务自身要求）；暴露给大模型的工具直接取自远程服务的 `tools/list` 真实清单（外加 Agent 内置的 `render_chart` 图表工具），因此可对接任意能力的 MCP 服务；系统提示词切换为通用助手模式，引导模型按工具定义自行编排调用。

### 运行日志（每个环节请求留痕）

Agent 在每个关键环节都会输出带时间戳的日志，便于排查「分析过程 / 工具返回 / 流式链路」问题：

- **HTTP 层**：每次 `/api/ask` 等请求的方法、路径、耗时、来源 IP；收到提问与回答完成时记录用户、会话、问题摘要与答案长度。
- **LLM 层**：每次大模型请求/流式请求的地址、模型、耗时；失败记录错误。
- **MCP 层**：每次工具调用的名称、参数、返回长度；返回错误/失败单独标记。
- **Agent 层**：MCP 登录、额外 MCP 对接、模型请求的工具调用与返回摘要。

日志默认只打印到控制台；在后台「运行日志」分组勾选 **保存日志到文件** 即可把以上日志同时追加写入 `logs/agent-YYYY-MM-DD.log`（按天滚动）。该开关可在后台**免重启热更新**，立即生效。

### 技能（Skill）与热加载

技能是一段"预定义工作流提示词"，由大模型通过 `use_skill` 工具按需加载并据其指引执行（与 ReAct 循环兼容）。内置技能位于 `skills/` 目录（`.md` 文件），覆盖常见企业数据分析场景：

| 技能 | name | 适用场景 |
|---|---|---|
| 销售业绩分析 | `sales_analysis` | GMV/订单数/客单价、趋势、同环比 |
| 客户 RFM 分群 | `customer_rfm` | 用户价值分层、VIP/挽留客户识别 |
| 留存同期群 | `retention_cohort` | 复购率、cohort 留存矩阵 |
| 转化漏斗 | `funnel_conversion` | 下单→支付→完成转化、瓶颈定位 |
| ABC 帕累托 | `abc_classification` | 贡献度分析、抓大放小 |
| 区域/门店对比 | `region_store_compare` | 区域/门店排行、结构差异 |
| 异常监控 | `anomaly_monitor` | 日/周 GMV 突增突降、预警归因 |
| 流失预警 | `churn_risk` | 沉睡/流失客户识别、唤醒 |
| 数据质量检查 | `data_quality_check` | 空值/异常/重复检测 |
| 周报生成 | `weekly_report` | 自动汇总周期经营指标 |

技能文件格式（`skills/xxx.md`）：

```markdown
---
name: sales_analysis
description: 当用户想做销售业绩、GMV、客单价、趋势或同环比分析时使用
---

## 工作流
1. 用 run_sql 统计周期内的订单数与总金额……
2. 用 render_chart 生成趋势图……
```

**热加载（无需重启进程）**：修改或新增技能文件后，有两种方式让 Agent 立即生效：

1. **后台一键重载**（推荐）：在后台「技能管理」分组调用 `POST /api/admin/reload-skills`（需 `skill:write` 权限，内置 admin/viewer 已具备）。返回当前已加载技能列表。
   ```powershell
   curl -X POST -H "Authorization: Bearer <token>" `
     http://localhost:8088/api/admin/reload-skills
   ```
2. **改配置自动重载**：在后台修改 `skills_dir`（技能目录路径）并保存，`ApplyConfig` 检测到目录变化会自动重新加载技能。

重载过程会写一条 `skill_load` 内部活动日志（可在 `GET /api/admin/activity-logs?kind=skill_load` 查看），失败不致命——保留上次成功加载的技能集合并告警。

> 说明：技能仅在 HTTP 服务模式（`-serve`）下可后台热加载；CLI 模式每次启动重新扫描目录，无需热加载。

### 连接真实 MySQL（做数据分析）

把 `mcp.db_dialect` 改为 `mysql`，`db_dsn` 填好连接串即可：

```json
"mcp": {
  "server_path": "../mcp-data-server/main.exe",
  "db_dialect": "mysql",
  "db_dsn": "root:password@tcp(127.0.0.1:3306)/analytics?charset=utf8mb4&parseTime=true",
  "username": "admin",
  "password": "admin123"
}
```

> 权限说明：默认 `admin` 是 `super_admin`，可调用 `run_sql` 执行任意只读 SQL；
> 若改用 `analyst1/analyst123` 等角色，则只能使用 `query_table`（结构化安全查询，自动叠加租户/区域/门店隔离并脱敏）。

### 文件 / 目录读写工具（内置 mcp-data-server 提供）

Agent 不做文件 I/O 实现，统一通过内置 `mcp-data-server` 暴露的文件工具完成，由 MCP 端做沙箱隔离与安全校验。暴露给大模型的工具包括：

- `read_file(path, max_bytes?)` / `write_file(path, content)` / `append_file(path, content)`
- `list_dir(path?)` / `make_dir(path)` / `delete_file(path)` / `read_dir_tree(path?)`

这些工具的实现全部封装在 `mcp-data-server`（见其 README「文件读写配置」），所有路径相对 `work_dir` 沙箱，禁止越界访问。Agent 端仅负责把这些工具名/参数映射给模型，并在调用时自动注入登录 `token`。

## 运行

### 前置条件
本地已部署并启动大模型服务，例如 Ollama：

```powershell
ollama serve
ollama pull qwen2.5:14b      # 或 llama3.1:8b 等
```

### 交互模式（REPL）

```powershell
.\data-analysis-agent.exe -config config.json
你> 各状态订单的数量和总金额分别是多少？
助手> （模型生成 SQL → MCP 执行 → 模型分析后输出结论）
你> exit
```

### 单次提问

```powershell
.\data-analysis-agent.exe -q "华东零售集团 paid 订单的总金额是多少？" -model qwen2.5:14b
```

### Web UI（聊天页面，自适应）

启动 HTTP 服务模式后，浏览器打开 `http://localhost:8088/` 即可使用自包含的聊天页面（无需 `npm build`，已通过 `embed` 内嵌进二进制）：

```powershell
.\data-analysis-agent.exe -serve -addr :8088
```

聊天页面特性：

- **自适应布局**：桌面端居中卡片、移动端全宽，顶栏/输入区在小屏自动精简。
- **基础设置抽屉**（右上角 ⚙）：可设置本次会话的 **模型 Model**、**生成温度 Temperature**、**最大 Token MaxTokens**，以及「默认展开分析过程 / 深色主题 / 自动滚动」等偏好；设置保存在浏览器 `localStorage`，刷新不丢。这些覆盖项只影响当次请求，不改动服务端运行配置。
- **流式思考与回答**：请求一发出即显示「思考中…」标识（带跳动圆点），避免 LLM 推理空窗期像卡死；最终回答以打字机方式逐字增量渲染（Markdown 即时格式化）。
- **工具调用流式展示**：模型每次调用工具时，先弹出「🔧 工具名 … 调用中…」卡片（带旋转 spinner）并立即展示入参；工具执行期间，MCP 服务端逐行读取数据并通过 `notifications/progress` 把进度（如「已读取 1200 行」）实时推回，前端在卡片上**逐条刷新进度**；即便无真实进度，Agent 也会每 ~0.9s 发一次「工具执行中…」心跳，杜绝像卡死。结果返回后原地补全参数与返回。多步工具依次追加，形成可实时跟进的「分析过程」。
- **分析过程完整不截断**：展开的「分析过程」面板完整展示每一步的工具名 / 参数 / 返回结果，超长结果默认限高并支持「展开全部 ▾ / 收起 ▴」；历史会话回放同样完整可展开。
- **图表输出开关**：设置抽屉内可勾选「支持图表输出」；关闭时本轮请求不向模型暴露 `render_chart` 工具、且不返回图表，端到端生效，刷新不丢。
- **富文本结果**：支持 Markdown 渲染、表格展示、SQL 代码块，以及由 `render_chart` 工具驱动的 **Canvas 图表**（柱状 / 折线 / 饼图，随窗口自适应重绘）。
- **后台入口**：页内「后台」按钮跳转 `/admin`，可在数据库中配置 MCP / 提示词等并热更新。

> 命令行也支持同样的基础设置：`-model`、`-temperature`、`-max-tokens` 三个 flag 会作为单次覆盖项生效（REPL 模式下整轮会话沿用）。

若另行构建了 Vue 前端（`web/` 目录），产物会挂载在 `/app/` 路径下，与内嵌聊天页共存、互不冲突。

## 工作流程示例

1. Agent 启动 → 拉起 `mcp-data-server` 子进程（若开启本地）→ `initialize` 握手 → `tools/list` 拉取工具 → 用配置账号 `auth_login` 获取 token（远程模式跳过）。
2. 预加载 `customers` / `orders` 等表结构，注入系统提示词。
3. 用户输入：「各状态订单的数量和金额分布」。
4. Agent 把问题 + 表结构发给本地大模型 → 模型决定调用 `run_sql`，生成
   `SELECT status, COUNT(*) cnt, SUM(amount) total FROM orders GROUP BY status`。
5. Agent 注入 token 后转发给 MCP → `mcp-data-server` 做权限校验 + 危险 SQL 拦截 + 审计 → 查 MySQL → 返回结果。
6. 结果回灌给大模型 → 模型给出业务分析与建议 → 输出给用户。

## 数据库日志与查询

除控制台/文件日志外，Agent 把所有关键环节**持久化到 SQLite 用户库**（`users.db`），可在后台按维度查询，便于审计与排障。涉及以下表：

| 表 | 内容 | 后台查询 API（需对应权限） |
|---|---|---|
| `request_logs` | 所有 HTTP 请求（方法/路径/路由/状态码/耗时/IP/用户） | `GET /api/admin/request-logs`（`request_log:read`） |
| `llm_call_logs` | 每次大模型调用（模型/请求/响应/耗时/错误） | `GET /api/admin/llm-logs`（`llm_log:read`） |
| `mcp_call_logs` | 每次 MCP 工具调用（工具名/参数/结果/耗时/错误，含初始化预加载表结构，以 `user_id=system` 标记） | `GET /api/admin/mcp-logs`（`mcp_log:read`） |
| `agent_activity_logs` | Agent 内部活动（MCP 连接/登录、技能加载、初始化预加载表结构） | `GET /api/admin/activity-logs`（`activity_log:read`） |
| `messages` / 会话表 | 多轮对话原文与富结果回放 | `GET /api/admin/chat-logs`（`chat_log:read`） |

筛选示例：

```powershell
# 查询所有 4xx/5xx 请求
curl -H "Authorization: Bearer <token>" `
  "http://localhost:8088/api/admin/request-logs?status=-400"

# 查看初始化阶段的 MCP 连接与技能加载
curl -H "Authorization: Bearer <token>" `
  "http://localhost:8088/api/admin/activity-logs?kind=mcp_connect"

# 查看初始化预加载表结构（首次调用 MCP 获取表结构）
curl -H "Authorization: Bearer <token>" `
  "http://localhost:8088/api/admin/activity-logs?kind=schema_load"
```

> 注意：HTTP 请求日志、LLM/MCP 调用日志、内部活动日志均在 HTTP 服务模式（`-serve`）下落库；CLI 模式不写数据库日志。

## 测试

```powershell
go test ./mcpclient/ -run TestHandshake -v
```

该测试会拉起 `mcp-data-server` 子进程，验证 `initialize` / `tools/list` / `auth_login` / `describe_table` / `run_sql` 全链路可用（已通过）。

## 设计要点

- **权限收口在 MCP**：Agent 只负责「理解意图 + 生成 SQL + 分析」，所有权限/隔离/脱敏/审计由 `mcp-data-server` 统一处理，模型无法绕过。
- **token 由 Agent 注入**：LLM 看到的工具不需要 token 参数，token 在 `executeTool` 中自动注入，避免泄露凭据。
- **结果处理（阈值可后台配置、默认不截断）**：喂给大模型的工具返回最多保留 `max_result_rows` 行（防上下文膨胀，0=不限制）；非 JSON 场景的字符截断上限 `max_result_chars` 与日志预览字符上限 `log_preview_chars` 均可在后台配置，**默认 0 = 不截断**，未配置即保留完整内容。展示层（聊天页「分析过程」）始终不截断，超长内容可展开查看。
- **可切换模型/协议**：改 `config.json` 即可在 Ollama / OpenAI 兼容服务之间切换，无需改代码。
- **MCP 多路聚合**：本地、主远程、额外远程的工具统一暴露、按名路由；任一 MCP 配置变化（开关、地址、`extra` 列表）经 `ApplyConfig` 自动重建连接，免重启生效。
