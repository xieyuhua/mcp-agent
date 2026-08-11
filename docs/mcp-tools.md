# MCP 工具清单

全部工具通过 `mcp-go` 注册，支持 stdio 与 HTTP（streamable-http / SSE）两种传输。

## 数据查询类

这四个工具都接受可选的 `origin_role` 参数，**服务端据此强制执行数据权限**。详见 [permission.md](permission.md)。

### `list_schema`

列出全部已开放表的结构元数据（表注释、字段注释、表关联关系）。

**大模型应优先调用本工具**了解可用数据，而非直连数据库探测。返回内容已按角色过滤掉不可见字段。

| 参数 | 必填 | 说明 |
|------|------|------|
| `origin_role` | 否 | 调用者角色标识 |

### `describe_table`

查看单张表的结构。

| 参数 | 必填 | 说明 |
|------|------|------|
| `origin_role` | 否 | 调用者角色标识 |
| `table` | 是 | 表名 |

### `query_table`

结构化安全查询。自动应用行级数据权限与字段脱敏，列名经白名单校验。

| 参数 | 必填 | 说明 |
|------|------|------|
| `origin_role` | 否 | 调用者角色标识 |
| `table` | 是 | 表名 |
| `fields` | 否 | 返回字段数组，留空返回全部 |
| `filters` | 否 | 等值过滤，如 `{"status":"paid"}` |
| `order` | 否 | 排序，如 `created_at desc` |
| `limit` | 否 | 行数上限，默认 100，最大 1000 |
| `offset` | 否 | 偏移 |

示例：

```json
{
  "origin_role": "tenant_admin",
  "table": "orders",
  "fields": ["id", "amount", "status"],
  "filters": { "status": "paid" },
  "order": "created_at desc",
  "limit": 20
}
```

### `run_sql`

执行原生只读 SQL。使用 gosqlx 自动注入行级权限 `where`（适配别名、多表关联、子查询），并做只读安全校验与字段脱敏。

| 参数 | 必填 | 说明 |
|------|------|------|
| `origin_role` | 否 | 调用者角色标识 |
| `sql` | 是 | SELECT 语句 |

约束：仅允许 SELECT；禁止注释；拦截 `insert/update/delete/drop/alter/create/truncate` 等危险关键字；只能访问后台已启用的表。

## 文件类（沙箱在 `work_dir` 内）

| 工具 | 参数 | 说明 |
|------|------|------|
| `read_file` | `path`, `max_bytes?` | 读取文本，默认 64KB，最大 1MB |
| `write_file` | `path`, `content` | 覆盖写入，自动建父目录 |
| `append_file` | `path`, `content` | 追加写入 |
| `list_dir` | `path?` | 列目录（名称/类型/大小/修改时间） |
| `make_dir` | `path` | 创建多级目录 |
| `delete_file` | `path` | 删除文件（不删目录） |
| `read_dir_tree` | `path?` | 递归目录树，最多两层 |

## 联网类

| 工具 | 参数 | 说明 |
|------|------|------|
| `web_search` | `query`, `limit?` | 搜索，默认 5 条，最大 10 |
| `web_fetch` | `url`, `max_chars?` | 抓取网页正文，默认 8000 字符，最大 40000 |
| `query_weather` | `location` | 实时天气（温度/体感/湿度/气压/风速） |

## 系统信息类

| 工具 | 参数 | 说明 |
|------|------|------|
| `system_info` | — | OS / 架构 / CPU 核数 / Go 版本 / 主机名 / 发行版 |
| `system_dirs` | — | 标准系统目录识别并标记是否存在 |
| `read_system_dir` | `path?`, `max_entries?` | 浏览任意目录，**不受沙箱限制**，默认 500 条最大 2000 |
| `get_env` | `name?` | 读环境变量，留空返回全部 |
| `disk_usage` | `path?` | 磁盘空间（总量/已用/可用/使用率） |

## 其他说明

### 错误处理

业务错误通过 `IsError=true` 的结果回传（符合 MCP 规范），而非 Go error。

### 进度推送

耗时工具（查询、搜索、抓取）会通过 `notifications/progress` 推送执行进度，实现分析过程的流式展示。推送失败会被静默忽略，不影响主流程。

### 审计

每次工具调用都会写入 `audit_logs` 表。
