# Agent 数据分析助手 + MCP 数据服务

一个企业级数据分析智能体：自然语言 → 本地大模型 → 生成 SQL → MCP 服务做**权限隔离 / 数据脱敏 / 危险 SQL 拦截 / 审计** → 大模型分析 → 输出结论与图表。

- `data-analysis-agent`：Agent 主体（Go），对接本地大模型（Ollama / OpenAI 兼容），编排工具调用。
- `mcp-data-server`：MCP 数据服务（Go），提供数据库查询/权限/脱敏能力，支持 **stdio（内置）** 与 **HTTP（对接任意标准 Agent）** 两种传输。

## 目录结构

```
agent/
├── start.bat / start.sh   # 一键启动（编译 + 运行两个服务）
├── mcp-data-server/       # MCP 数据服务
│   ├── cmd/server/main.go # 入口，支持 -config，transport=stdio|http|both
│   ├── config.http.json   # HTTP 模式配置示例（:9000）
│   ├── internal/
│   │   ├── transport/     # stdio 与 http（streamable-http + sse）传输层
│   │   ├── admin/         # 权限后台 REST API
│   │   ├── web/           # Web 资源 embed 打包 + 静态服务（含 assets/）
│   │   ├── handler/       # MCP 工具定义与处理器
│   │   ├── service/       # 业务服务（查询/权限/审计）
│   │   ├── auth/          # 鉴权与权限策略
│   │   └── mask/          # 脱敏
│   └── README.md          # 服务详细说明
└── data-analysis-agent/   # Agent 主体
    ├── config.json        # 默认配置（local 内置模式，首次运行作为数据库种子）
    ├── config.remote.json # remote 对接模式示例（对接 HTTP MCP）
    ├── agent/             # Agent 编排 + 工具 + 天气查询
    ├── mcpclient/         # MCP 客户端（stdio 本地 / http 远程）
    ├── llm/               # 本地大模型客户端（Ollama / OpenAI）
    ├── server/            # HTTP 服务模式（供前端调用）
    ├── internal/
    │   ├── confdb/        # 配置持久化（SQLite 配置表 + 内存缓存）
    │   └── admin/         # Agent 后台管理（登录 / 配置 CRUD / 内嵌页面）
    └── README.md          # Agent 详细说明
```

## 前置依赖

- **Go 1.22+**（编译两个服务）
- **Ollama**（本地大模型，默认 `qwen3:8b`）：
  ```bash
  ollama pull qwen3:8b
  ```
  若用其他 OpenAI 兼容服务，改 `config.json` 的 `llm` 段即可。

## 快速开始

### 方式一：一键启动（推荐）

```bash
# Windows
start.bat
# Linux / Mac
chmod +x start.sh && ./start.sh
```

脚本会：
1. 编译 `mcp-data-server`（HTTP 模式，监听 `:9000`）
2. 编译 `data-analysis-agent`（remote 模式对接上面的 MCP，监听 `:8088`）
3. 启动两个服务

启动后：
- Agent API：`http://localhost:8088`（POST `/api/ask`）
- Agent 后台配置：`http://localhost:8088/admin`（浏览器打开，用 `admin / admin123` 登录，可配置 LLM / MCP / Agent 参数 / 系统提示词，即时生效）
- MCP 权限后台：`http://localhost:9000`（浏览器打开，用 `admin / admin123` 登录配置权限）

### 方式二：内置模式（mcp-data-server 作为子进程）

```bash
cd data-analysis-agent
go build -o data-analysis-agent.exe .
# 默认 config.json 即为 local 模式，会拉起 ../mcp-data-server/main.exe 子进程
.\data-analysis-agent.exe -serve -addr :8088
```

或直接提问（单次模式）：
```bash
.\data-analysis-agent.exe -q "北京现在天气怎么样？"
.\data-analysis-agent.exe -q "各状态订单数量与金额分别是多少？"
```

### 方式三：仅启动 MCP 服务（HTTP，供其他 Agent 对接）

```bash
cd mcp-data-server
go build -o main.exe ./cmd/server
.\main.exe -config config.http.json
# 或同时支持 stdio + http：
# TRANSPORT=both HTTP_ADDR=:9000 .\main.exe
```

## Web 聊天界面

启动 Agent 的 HTTP 服务模式（`./data-analysis-agent -serve -addr :8088`）后，浏览器打开 `http://localhost:8088/` 即可使用自包含聊天页（已通过 `embed` 内嵌进二进制，无需 `npm build`）。

界面能力：

- **流式思考与回答**：请求发出即显示「思考中…」跳动圆点，避免 LLM 推理空窗期像卡死；最终回答以打字机方式逐字增量渲染（Markdown 即时格式化）。
- **工具调用流式展示**：模型每次调用工具先弹出「🔧 工具名 … 调用中…」卡片（旋转 spinner）并立即展示入参；执行期间 MCP 服务端通过 `notifications/progress` 把进度（如「已读取 1200 行」）实时推回，前端逐条刷新；无真实进度时 Agent 每 ~0.9s 发「工具执行中…」心跳，杜绝卡死。结果返回后原地补全，多步依次追加成可实时跟进的「分析过程」。
- **分析过程完整不截断**：展开的「分析过程」面板完整展示每步工具名 / 参数 / 返回（后端不再按字符截断），超长结果限高并可「展开全部 ▾ / 收起 ▴」；历史会话回放同样完整。
- **图表输出开关**：设置抽屉（⚙）内可勾选「支持图表输出」，关闭时本轮不暴露 `render_chart` 工具且不返回图表，端到端生效，设置存于浏览器 `localStorage`。
- **基础设置抽屉**：可设本次会话的模型 / 温度 / 最大 Token，以及默认展开分析过程、深色主题、自动滚动等偏好；仅影响当次请求，不改服务端配置。
- **富文本结果**：Markdown、表格、SQL 代码块，以及 `render_chart` 驱动的 Canvas 图表（柱 / 折线 / 饼图，随窗口自适应重绘）。
- **后台入口**：页内「后台」跳转 `/admin`，可在数据库中配置 MCP / 提示词等并热更新。

> 命令行（REPL / 单次提问）同样支持流式：工具调用发起即打印工具名与参数，结果不截断完整输出。

## 配置说明

### data-analysis-agent / config.json

```jsonc
{
  "llm": {
    "provider": "ollama",          // ollama | openai
    "base_url": "http://localhost:11434",
    "model": "qwen3:8b",
    "api_key": ""                  // openai 兼容服务填 token
  },
  "mcp": {
    "mode": "local",               // local(内置子进程) | remote(远程 MCP)
    "server_path": "../mcp-data-server/main.exe",  // local 模式子进程路径
    "base_url": "http://127.0.0.1:9000/mcp",       // remote 模式地址
    "transport": "streamable-http",                // remote: streamable-http | sse
    "username": "admin",           // local 模式登录账号
    "password": "admin123"
  }
}
```

### mcp-data-server / config.http.json

```jsonc
{
  "db_dialect": "sqlite",          // sqlite(演示) | mysql(真实分析)
  "db_dsn": "./data.db",
  "transport": "http",             // stdio | http | both
  "http_addr": ":9000",            // HTTP 监听地址
  "mask_enabled": true,            // 是否脱敏
  "seed_demo": true                // 是否写入演示数据
}
```

## Agent 后台配置（数据库化，推荐）

Agent 的运行配置（LLM / MCP / Agent 参数 / 系统提示词 / 后台凭据）不再写死在代码或文件里，
而是持久化到 SQLite（`agent.db`，首次运行从 `config.json` 播种），可通过后台页面**免重启热更新**：

- 打开 `http://localhost:8088/admin` → 用 `admin / admin123` 登录。
- 可配置分组：
  - **LLM 大模型**：提供方、地址、模型名、API Key、温度、最大 Token。
  - **MCP 对接**：local（子进程路径 / 数据库类型 / 连接串 / 脱敏 / 演示数据 / 账号密码）或 remote（地址 / 传输方式 / 鉴权 Key）。
  - **Agent 编排**：最大推理步数、是否原生工具调用、结果最大行数。
  - **运行日志**：是否把每个环节（HTTP 请求 / LLM 调用 / MCP 工具调用 / 工具返回）的日志带时间戳保存到文件（`logs/agent-YYYY-MM-DD.log`）；关闭则仅打印到控制台。开关可后台热更新，无需重启。
  - **系统提示词**：内置数据库分析场景、远程 MCP 场景（可直接编辑，不再写死）。
  - **后台凭据**：后台登录账号 / 密码。
- 点击「保存并应用」：写入数据库并即时热更新到运行中的 Agent（LLM 立即换模型；改 MCP 相关项会自动重连 MCP 服务）；「恢复默认」可重置为内置默认值。
- 后台 API（需 Bearer token）：`POST /api/admin/login`、`GET/PUT /api/admin/config`、`POST /api/admin/reset`。

## 验证

两个服务均提供健康检查：
- `GET http://localhost:8088/api/health` → `{"status":"ok"}`
- `GET http://localhost:9000/` → 权限后台页面（200）

## 常见问题

- **Agent 启动报 LLM 连接失败**：确认 Ollama 已启动且模型已拉取（`ollama pull qwen3:8b`）。
- **remote 模式连不上 MCP**：确认 mcp-data-server 已以 `transport=http` 启动，且 `base_url` 端口一致（默认 9000）。
- **权限后台 403**：用 `admin / admin123` 登录获取 token，后台 API 仅 `super_admin` 可访问。
- **Agent 后台打不开 / 改配置报未授权**：Agent 后台在 `http://localhost:8088/admin`，用 `admin / admin123` 登录；API 需在 Header 带 `Authorization: Bearer <token>`。
- **想改系统提示词/模型但不想重启**：直接在 `http://localhost:8088/admin` 修改并「保存并应用」即可，无需重启进程。
- **分析过程看不到完整工具返回 / 像卡死**：确保使用最新二进制——现已支持「思考中…」标识、工具「调用中…」实时卡片，且分析过程结果**不截断**完整展示（超长可展开）。若页面仍异常，强刷浏览器（Ctrl+F5）以加载最新内嵌页面。
