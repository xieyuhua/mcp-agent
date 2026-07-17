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
    ├── config.json        # 默认配置（local 内置模式）
    ├── config.remote.json # remote 对接模式示例（对接 HTTP MCP）
    ├── agent/             # Agent 编排 + 工具 + 天气查询
    ├── mcpclient/         # MCP 客户端（stdio 本地 / http 远程）
    ├── llm/               # 本地大模型客户端（Ollama / OpenAI）
    ├── server/            # HTTP 服务模式（供前端调用）
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

## 验证

两个服务均提供健康检查：
- `GET http://localhost:8088/api/health` → `{"status":"ok"}`
- `GET http://localhost:9000/` → 权限后台页面（200）

## 常见问题

- **Agent 启动报 LLM 连接失败**：确认 Ollama 已启动且模型已拉取（`ollama pull qwen3:8b`）。
- **remote 模式连不上 MCP**：确认 mcp-data-server 已以 `transport=http` 启动，且 `base_url` 端口一致（默认 9000）。
- **权限后台 403**：用 `admin / admin123` 登录获取 token，后台 API 仅 `super_admin` 可访问。
