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
- **MCP 对接（两种方案）**：Agent 作为 MCP **客户端**，支持：
  - **本地内置**（`mode=local`）：自动拉起 `mcp-data-server` 子进程（stdio JSON-RPC），复用其「权限隔离 + 数据脱敏 + 危险 SQL 拦截 + 审计」能力。
  - **远程对接**（`mode=remote`）：对接任意远程 MCP 服务，支持 `streamable-http`（默认，推荐）与旧版 `sse` 两种传输协议，可配置鉴权头。
- **Tools 连接 MySQL**：通过 MCP 后端的 `query_table` / `run_sql` 工具真正落到 MySQL（或 SQLite 演示库）执行查询。
- **ReAct 编排**：模型可多轮调用工具（先看表结构 → 生成 SQL → 取数 → 分析），直到给出最终结论。
- **零外部依赖**：仅用 Go 标准库实现 HTTP 与 MCP 通信，构建无需联网拉包。

## 目录结构

```
data-analysis-agent/
├── main.go                 # 入口：REPL / 单次提问
├── config.json             # 运行配置
├── config/                 # 配置加载
├── mcpclient/              # MCP 客户端（传输层抽象：stdio 本地 / http 远程）
├── llm/                    # 本地大模型客户端（Ollama / OpenAI 兼容）
└── agent/                  # Agent 编排（ReAct 循环 + 工具定义 + 权限处理）
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
    "mode": "local",                      // local(内置子进程) | remote(远程 MCP 服务)
    "server_path": "../mcp-data-server/main.exe",
    "db_dialect": "sqlite",               // sqlite(演示) | mysql(真实分析)
    "db_dsn": "./data.db",                // mysql: user:pass@tcp(127.0.0.1:3306)/db?charset=utf8mb4&parseTime=true
    "username": "admin",                  // 登录账号（Agent 自动登录获取 token）
    "password": "admin123",
    "mask_enabled": true,                 // 是否启用脱敏
    "seed_demo": true,                    // 是否写入演示数据
    "base_url": "http://127.0.0.1:9000/mcp", // remote 模式：远程 MCP 地址
    "transport": "streamable-http",       // remote 模式：streamable-http(默认) | sse
    "api_key": ""                         // remote 模式：可选 Bearer 鉴权
  },
  "agent": {
    "max_steps": 8,                       // ReAct 最大步数
    "use_native_tools": true,             // 是否使用原生 function calling
    "max_result_rows": 200                // 单次工具返回最多保留行数
  }
}
```

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

### 远程 MCP 对接（mode=remote）

除了内置 `mcp-data-server` 子进程，Agent 还能直接对接任意**远程 MCP 服务**（如另一台机器上的 MCP Server、第三方 MCP 网关等）。配置 `mcp.mode=remote` 即可：

```json
"mcp": {
  "mode": "remote",
  "base_url": "http://192.168.1.10:9000/mcp",
  "transport": "streamable-http",
  "api_key": "可选Bearer令牌",
  "headers": { "X-Custom": "value" }
}
```

- **transport=streamable-http（默认，推荐）**：通过 `POST` 发送 JSON-RPC，服务端可在响应中以 `text/event-stream`（SSE 流）或普通 JSON 返回结果，自动处理 `Mcp-Session-Id` 会话头。
- **transport=sse（旧版协议）**：先 `GET /sse` 建立接收流，再 `POST /messages` 发送请求，响应经 SSE 流异步回传并按 id 匹配。

对接远程服务时：
- 不再拉起本地子进程，也不做 `auth_login` / token 注入（除非远程服务自身要求，由对应实现处理）。
- 暴露给大模型的工具**直接取自远程服务的 `tools/list` 真实清单**（外加内置的 `query_weather` 天气查询与 `render_chart` 图表工具），因此可对接任意能力的 MCP 服务。
- 系统提示词切换为通用助手模式，引导模型按工具定义自行编排调用。

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

## 工作流程示例

1. Agent 启动 → 拉起 `mcp-data-server` 子进程 → `initialize` 握手 → `tools/list` 拉取工具 → 用配置账号 `auth_login` 获取 token。
2. 预加载 `customers` / `orders` 等表结构，注入系统提示词。
3. 用户输入：「各状态订单的数量和金额分布」。
4. Agent 把问题 + 表结构发给本地大模型 → 模型决定调用 `run_sql`，生成
   `SELECT status, COUNT(*) cnt, SUM(amount) total FROM orders GROUP BY status`。
5. Agent 注入 token 后转发给 MCP → `mcp-data-server` 做权限校验 + 危险 SQL 拦截 + 审计 → 查 MySQL → 返回结果。
6. 结果回灌给大模型 → 模型给出业务分析与建议 → 输出给用户。

## 测试

```powershell
go test ./mcpclient/ -run TestHandshake -v
```

该测试会拉起 `mcp-data-server` 子进程，验证 `initialize` / `tools/list` / `auth_login` / `describe_table` / `run_sql` 全链路可用（已通过）。

## 设计要点

- **权限收口在 MCP**：Agent 只负责「理解意图 + 生成 SQL + 分析」，所有权限/隔离/脱敏/审计由 `mcp-data-server` 统一处理，模型无法绕过。
- **token 由 Agent 注入**：LLM 看到的工具不需要 token 参数，token 在 `executeTool` 中自动注入，避免泄露凭据。
- **结果截断**：单次工具返回最多保留 `max_result_rows` 行，防止上下文膨胀。
- **可切换模型/协议**：改 `config.json` 即可在 Ollama / OpenAI 兼容服务之间切换，无需改代码。
