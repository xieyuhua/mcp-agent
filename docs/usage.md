# 数据查询 MCP 服务 · 详细使用文档

本文档面向 **运维 / 实施 / 开发者**，说明：

1. 账号登录、密码修改、退出登录（后台管理）
2. 数据如何流转（用户提问 → Agent → MCP → 数据库 → 结果回传）
3. 参数如何传递（Agent 与 MCP 之间、HTTP 与 stdio 之间）
4. Agent 与 MCP 之间的鉴权与角色链路（`origin_role` / `user_id` / 会话 Cookie）

> 配套文档：
> - 后台管理：见 `docs/admin.md`
> - MCP 工具清单：见 `docs/mcp-tools.md`
> - 业务权限模型（行级 where、字段脱敏、关联）：见 `docs/business-permission.md`

---

## 0. 系统角色总览

```
┌──────────────┐   提问/HTTP     ┌──────────────────┐   MCP(stdio/http)   ┌──────────────────┐   业务SQL    ┌──────────────┐
│  前端浏览器   │ ───────────────▶│ data-analysis-   │ ──────────────────▶│  mcp-data-server │ ────────────▶│ 目标业务数据库 │
│ (Vue)        │ ◀───────────────│ agent (Go)       │ ◀──────────────────│  (Go + GORM)    │ ◀────────────│ oracle/      │
│ 登录 / 提问   │   回答/流式      │ Agent + LLM      │   工具结果(文本)     │ 查询引擎 + 权限  │   行/列       │ mysql/sqlite │
└──────────────┘                 └──────────────────┘                    └──────────────────┘              └──────────────┘
        │                                  │ 后台管理(HTTP)                      │ 后台管理(HTTP)
        └──────────────────────────────────┴────────────────────────────────────┘
                                      ┌──────────────────┐
                                      │ web/admin (Vue)  │  登录 / 改密 / 导入表结构 / 配置权限
                                      └──────────────────┘
```

**两个服务、三套登录体系**：

| 体系 | 服务对象 | 存储 | 鉴权方式 |
|------|---------|------|---------|
| ① 后台管理登录 | `web/admin` 管理员 | mcp-data-server 的 `admin_users` 表（sha256+salt，HMAC 签名 Cookie） | `mcp-data-server` 的 `/api/admin/*` |
| ② 终端用户登录 | `data-analysis-agent` 的 Web 用户 | agent 的 `users` 表 | Bearer Token（`Authorization`） |
| ③ Agent ↔ MCP 内部调用 | Agent 程序 → MCP 工具 | 不持久化（内存会话/Cookie） | 见第 5 节 |

> 注意：体系①与体系②是**两套独立账号**。后台管理员（运维配表、配权限）与提问的终端用户（业务人员）互不相通。

---

## 1. 后台管理登录、改密与退出

### 1.1 首次安装：自动生成管理员账号

`mcp-data-server` 启动（`cmd/server/main.go`）时调用 `auth_service.EnsureBootstrapUser()`：

- 若 `admin_users` 表为空，自动创建账号 `admin`；
- 密码为 **12 位随机字符串**，并打印到启动日志（横幅）；
- 同时标记 `must_change_password = true`（强制首登改密，可由后台关闭）。

启动日志示例：

```
============================================================
 数据查询 MCP 服务已启动
 后台管理地址: http://localhost:9000/admin
 首次管理员账号: admin
 首次管理员密码: aZ3kP9qW2xY7   <-- 请尽快登录修改
 会话密钥(SESSION_SECRET): 已通过环境变量注入
============================================================
```

### 1.2 登录流程

```
浏览器 → POST /api/admin/login {username, password}
        → 校验 sha256(salt+password) == password_hash
        → 签发 HMAC 签名 HttpOnly Cookie (session, 24h)
        → 返回 {username, role, must_change_password}
```

前端 `web/admin/src/views/Login.vue` 收集账号密码，调用 `api.js` 的 `login()`：

```js
export function login(username, password) {
  return request.post('/api/admin/login', { username, password })
}
```

> Cookie 为 `HttpOnly`，JS 无法读取，防 XSS 窃取；24 小时过期后需重新登录。

### 1.3 修改密码（后台）

登录后，点击右上角用户名 →「修改密码」，弹出 `App.vue` 中的表单，调用 `changePassword()`：

```js
export function changePassword(oldPassword, newPassword) {
  return request.post('/api/admin/change-password', { old_password: oldPassword, new_password: newPassword })
}
```

后端 `auth_handler.changePassword` 逻辑：

1. 中间件 `requireLogin` 校验会话 Cookie；
2. 校验 `old_password` 正确；
3. 校验 `new_password` 强度（≥8 位，建议含大小写+数字）；
4. 重新生成 salt，计算 `sha256(salt + new_password)`，更新 `password_hash`；
5. **吊销所有旧会话**（重新签发会话 Cookie），`must_change_password` 置否。

> 改密后会话刷新，当前浏览器保持登录，其他已登录设备需重新登录。

### 1.4 退出登录（后台）

点击右上角「退出登录」，调用 `logout()`：

```js
export function logout() {
  return request.post('/api/admin/logout')
}
```

后端 `auth_handler.logout`：**服务端删除会话记录 + 清除浏览器 Cookie**（设置过期 Cookie），前端跳转回登录页。

### 1.5 受保护的后台接口

所有后台管理接口（表配置、字段注释、关联、权限、导入、目标库状态）都挂在 `authed` 路由组下，统一经过 `requireLogin` 中间件：

```
GET  /api/admin/tables
GET  /api/admin/fields?table=xxx
POST /api/admin/import-schema      ← 从目标库导入真实表/字段
GET  /api/admin/db-status          ← 目标库连通性 + 脱敏 DSN
POST /api/admin/relations
...（详见 admin.md）
```

未登录访问会返回 `401 {error:"未登录"}`，前端路由守卫（`router.js`）自动跳转登录页。

---

## 2. 数据流转总图（一次提问发生了什么）

以终端用户在 `data-analysis-agent` 的 Web 界面提问 **"华东区上个月销售额Top10的门店"** 为例：

```
① 浏览器                         ② data-analysis-agent                      ③ mcp-data-server                      ④ 目标业务数据库
─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
用户输入问题
   │  POST /api/ask
   ▼
                               1. 鉴权(体系② Bearer Token)
                               2. 加载用户记忆/会话历史
                               3. 组装 System Prompt
                                  （含后台配置的表结构、字段业务名、
                                   脱敏规则、关联 ON 表达式）
                               4. 调 LLM，进入 ReAct 循环：
                                  LLM 决定调用哪个工具
                                      │  mcp.CallTool("query_table", {...})
                                      ▼
                                                               5. 解析参数 → ctxFromArgs()
                                                                  取 origin_role / user_id
                                                               6. 查 TableConfig + FieldConfig
                                                                  拼出可读字段、行级 where
                                                               7. 应用 TableRelation
                                                                  (JOIN + ON 表达式)
                                                               8. 注入 desensitize_rules
                                                                  (字段脱敏)
                                                               9. 生成最终 SQL
                                                                  (SELECT ... FROM ... JOIN ... WHERE ...)
                                                                      │  gorm.OpenDB().Raw(sql)
                                                                      ▼
                                                                                           10. 执行只读查询
                                                                                               (关键字黑名单拦截)
                                                                                                  │  行+列结果
                                                                                                  ▼
                                                               11. 行级裁剪 + 字段脱敏
                                                               12. 序列化为文本返回
                                                                   (isError=false)
                                      │  工具结果文本(截断后)
                                      ▼
                               13. 把工具结果回灌 LLM 上下文
                               14. LLM 继续思考 → 可能再调工具 / 出图
                               15. 汇总最终答案 + SQL + 图表
   │  ◀── 流式回答(答案/步骤/图表) ──┘
   ▼
用户看到：自然语言结论 + 可查看 SQL + 图表
```

**关键落库（审计 / 追溯）**：

- `mcp_call_logs`：每次工具调用（工具名、参数脱敏、耗时、是否报错、user_id、conv_id）。
- agent 侧 `request_logs`：每次 HTTP 提问（用户、耗时、状态）。
- 后台 `web/admin` 可查看调用日志，用于排查"为什么某字段看不到 / 为什么数据被脱敏"。

---

## 3. Agent 与 MCP 之间的参数传递

### 3.1 传输方式

`data-analysis-agent` 通过 `mcpclient` 以**远程客户端**方式对接 `mcp-data-server`，仅支持 `streamable-http` / `sse` 远程传输（本地 stdio 子进程模式已移除）：

| 模式 | 触发 | 鉴权 | 典型用途 |
|------|------|------|---------|
| **streamable-http / sse（远程）** | `mcp.base_url=http://host:9000/mcp` | 可选 `api_key`(Bearer) | 服务分离部署 |

> `mcp-data-server` 自身仍可独立以 `stdio` 对外提供服务（供其它 MCP 客户端对接），但 `data-analysis-agent` 不再以子进程方式拉起它。

### 3.2 工具调用的入参（Agent → MCP）

Agent 调用 MCP 工具时，参数以 **JSON-RPC `arguments`** 传递。`tool_handler.ctxFromArgs` 会从参数中读取以下**鉴权/上下文字段**：

| 参数键 | 含义 | 缺省 |
|--------|------|------|
| `origin_role` （兼容 `role`） | 数据访问角色，决定行级 where、字段可见/脱敏 | `super_admin` |
| `user_id` | 调用者标识，写入审计日志 | `agent` |
| `tenant_id` | 多租户隔离字段（行级 where 拼接） | 空 |

Agent 在调用工具前，会把当前终端用户的 **数据角色** 注入为 `origin_role`。例如 `userdb.User.DataRole = "region_manager"` 时，Agent 传 `origin_role:"region_manager"`，MCP 据此拼接该角色后台配置的 `row_filter`（如 `region = '华东'`）。

> 这样实现了"同一句提问，不同角色看到不同数据"——权限完全由后台 `web/admin` 配置，不写死在代码里。

### 3.3 工具清单（tools/list）

`Start()` / `StartRemote()` 完成握手后会调用 `tools/list`，Agent 据此把 MCP 工具注入给 LLM 作为可调用函数。主要工具：

- `query_table`：按业务语义查询（推荐，自动套用权限/脱敏/关联）。
- `run_sql`：执行原生只读 SQL（仅 `super_admin` 可用）。
- `describe_table`：获取单表结构/字段注释。
- `auth_login`：HTTP 模式下 Agent 向 MCP 完成内部鉴权（如远程需要）。

### 3.4 调用示例（伪代码）

```go
// data-analysis-agent/agent/agent.go 中
params := map[string]interface{}{
    "table":      "orders",
    "filters":    []map[string]interface{}{{"field": "region", "op": "=", "value": "华东"}},
    "fields":     []string{"store_name", "amount"},
    "order_by":   "amount DESC",
    "limit":      10,
    "origin_role": user.DataRole,   // 关键：把用户数据角色传给 MCP
    "user_id":     user.ID,
}
text, isErr, err := a.mcp.CallTool("query_table", params, onProgress)
```

### 3.5 返回参数（MCP → Agent）

MCP 工具返回 JSON-RPC `content[].text`，Agent 的 `mcpclient.Client.CallTool` 会把多个 text 块拼接为字符串返回：

```go
type out struct {
    Content []struct {
        Type string `json:"type"`   // "text"
        Text string `json:"text"`
    } `json:"content"`
    IsError bool `json:"isError"`
}
```

Agent 拿到文本后：

1. `truncateResult()` 截断超长结果（防止上下文爆炸，阈值见 `cfg.Agent.MaxResultChars`）；
2. 作为 `role:"tool"` 消息回灌 LLM；
3. 若 `IsError=true`，记入 `mcp_call_logs` 的 `is_error=1`。

---

## 4. 鉴权链路全景（谁信谁）

```
[终端用户] --Bearer Token--> [agent /api/ask]             体系②
                                    │
                                    │ 内部调用
                                    │ origin_role / user_id
                                    ▼
                            [mcp-data-server 工具]          体系③（http 可选 api_key）
                                    │
                                    │ 行级 where + 脱敏规则（来自后台配置）
                                    ▼
                            [目标业务库查询]

[后台管理员] --HMAC Cookie--> [mcp-data-server /api/admin/*]  体系①
                                    │
                                    │ 读写 TableConfig / FieldConfig / 权限 / 关联
                                    ▼
                            [同一库的 admin_users / 配置表]
```

**三层互不越权**：

- 终端用户**永远不能**直接调后台 `/api/admin/*`（无 Cookie 会被 401）。
- 终端用户**不能**绕过权限直接跑 `run_sql`，除非其 `DataRole=super_admin`。
- Agent 调用 MCP 时即使不传 `origin_role`，也回落到 `super_admin`；后台可改为"拒绝匿名"以收紧。

---

## 5. 目标数据库（可配置方言）

后台 `web/admin` 顶栏实时显示目标库状态：`目标库：oracle ● 已连接`。

| 方言 | DSN 格式 | 驱动 |
|------|---------|------|
| sqlite | `data.db` 或绝对路径 | glebarez/sqlite（纯 Go） |
| mysql | `user:pass@tcp(host:3306)/dbname?...` | gorm mysql |
| oracle | `oracle://user:pass@host:1521/service_name` | godoes/gorm-oracle（纯 Go，无 CGO） |

配置方式（`mcp-data-server/config` 或环境变量 `DB_DIALECT` / `DB_DSN`）：

- `import-schema` 接口会**读取目标库真实表/字段**，自动生成草稿（业务注释留空），管理员再手动补充注释、业务名、敏感标记。
- Oracle 表名自动转小写存储，避免大小写敏感问题。

> 当前为 **单库架构**：后台权限元数据表与业务表在**同一个库**。若要"本地 sqlite 存后台配置 + 远程 oracle 存业务数据"双库分离，需另行拆分连接，不在本文档范围。

---

## 6. 常见问题排查

| 现象 | 可能原因 | 处理 |
|------|---------|------|
| 后台打开跳登录页 | Cookie 过期/未登录 | 重新登录；检查 `SESSION_SECRET` 是否变更（变更会使旧会话失效） |
| 改密后其他设备仍登录 | 未强制吊销 | 当前实现改密即吊销全部会话，若仍异常检查部署是否为多实例（会话存内存） |
| 查询返回空 / 字段被脱敏 | `origin_role` 对应的行级 where 过严 | 后台检查该角色的 `row_filter` 与字段 `desensitize` 配置 |
| `401 未登录` from agent→mcp | 远程模式缺 `api_key` | 在 agent `config.json` 的 `mcp.api_key` 填入 MCP 侧 Bearer |
| `tools/list` 为空 | 远程 MCP 未启动 / 地址或鉴权错误 | 看 agent 启动日志；确认 `mcp.base_url` 与 `mcp.api_key` 正确 |
| Oracle 连接失败 | DSN service_name 错 / 驱动纯 Go 限制 | 用 `db-status` 接口验证连通性；确认端口与 service_name |

---

## 7. 快速操作清单

**后台管理员首次上线**：
1. 启动 `mcp-data-server`，从日志复制 `admin / 随机密码`；
2. 打开 `/admin` 登录 → 立即「修改密码」；
3. 「从数据库导入」拉取真实表/字段；
4. 逐个字段补充业务名、注释、敏感标记；
5. 配置表关联（多字段 ON 表达式）与角色权限（行级 where、脱敏）；
6. 确认顶栏「目标库 ● 已连接」。

**终端用户使用**：
1. 在 `data-analysis-agent` 的 Web 注册/登录（体系②）；
2. 直接提问，Agent 自动经 MCP 查询并脱敏返回。

**退出后台登录**：右上角「退出登录」→ 清除会话 Cookie → 回到登录页。
