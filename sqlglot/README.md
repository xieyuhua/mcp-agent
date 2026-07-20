# SQL 解析 API 服务

基于 [sqlglot](https://github.com/tobymao/sqlglot) 的 SQL 解析服务，提供 **解析（Parse）**、**转译（Transpile）**、**格式化（Format）**、**校验（Validate）** 与 **元数据提取（Extract）** 能力。

同时支持 **REST（FastAPI）** 与 **gRPC** 两种调用方式，核心逻辑统一位于 `core.py`，两种协议共享同一套实现，保证行为一致。

---

## 目录

- [特性](#特性)
- [项目结构](#项目结构)
- [架构设计](#架构设计)
- [环境要求](#环境要求)
- [安装](#安装)
- [快速开始](#快速开始)
  - [REST 服务](#rest-服务)
  - [gRPC 服务](#grpc-服务)
- [接口说明](#接口说明)
- [REST 调用示例](#rest-调用示例)
- [gRPC 调用示例](#grpc-调用示例)
- [参数说明](#参数说明)
- [错误处理](#错误处理)
- [支持的方言](#支持的方言)
- [常见问题 FAQ](#常见问题-faq)

---

## 特性

- **一份逻辑，两种协议**：REST 与 gRPC 共享 `core.py`，无重复代码。
- **多方言支持**：MySQL、PostgreSQL、SQLite、BigQuery、Hive、Spark、Snowflake 等 30+ 方言，可自动识别。
- **AST 解析**：将 SQL 解析为结构化抽象语法树，并可导出为 JSON。
- **方言转译**：在不同数据库方言之间自动转换 SQL。
- **元数据提取**：一次性提取 SQL 涉及的表名、列名、函数、CTE。
- **交互式文档**：REST 自带 Swagger UI，开箱即用。

---

## 项目结构

| 文件 | 说明 |
| --- | --- |
| `core.py` | 核心解析逻辑，REST 与 gRPC 共享复用 |
| `main.py` | REST(FastAPI) 服务入口 |
| `grpc_server.py` | gRPC 服务端 |
| `grpc_client_example.py` | gRPC 客户端调用示例 |
| `sqlparser.proto` | gRPC 接口定义（IDL） |
| `sqlparser_pb2.py` / `sqlparser_pb2_grpc.py` | 由 proto 生成的 stub（勿手动修改） |
| `requirements.txt` | 依赖清单 |

---

## 架构设计

```
                    ┌──────────────────────┐
   HTTP / JSON ───▶ │  main.py (FastAPI)    │──┐
                    └──────────────────────┘  │
                                              ▼
                                    ┌──────────────────┐
                                    │   core.py        │  ← sqlglot
                                    │  (统一解析逻辑)   │
                                    └──────────────────┘
                                              ▲
                    ┌──────────────────────┐  │
   gRPC / Protobuf ─▶│ grpc_server.py       │──┘
                    └──────────────────────┘
```

- `core.py` 是唯一的业务逻辑层，所有 SQL 处理均在此完成，失败时抛出 `SqlError`。
- `main.py` 将 `SqlError` 适配为 HTTP 400；`grpc_server.py` 将其适配为 `INVALID_ARGUMENT`。
- 新增能力只需在 `core.py` 实现一次，再在两个入口暴露即可。

---

## 环境要求

- Python 3.10+
- 依赖见 `requirements.txt`

---

## 安装

```bash
pip install -r requirements.txt
```

---

## 快速开始

### REST 服务

```bash
uvicorn main:app --host 0.0.0.0 --port 8000 --reload
```

启动后访问交互式文档：<http://localhost:8000/docs>

### gRPC 服务

```bash
python grpc_server.py          # 默认监听 0.0.0.0:50051
```

另开终端运行客户端示例：

```bash
python grpc_client_example.py
```

> 修改 `sqlparser.proto` 后需重新生成 stub：
> ```bash
> python -m grpc_tools.protoc -I. --python_out=. --grpc_python_out=. sqlparser.proto
> ```

---

## 接口说明

REST 接口与 gRPC 方法一一对应，功能完全一致。

| 功能 | REST（方法 + 路径） | gRPC 方法 | 说明 |
| --- | --- | --- | --- |
| 健康检查 | `GET /health` | `Health` | 返回服务状态与 sqlglot 版本 |
| 方言列表 | `GET /dialects` | `Dialects` | 列出所有支持的方言 |
| 解析 AST | `POST /parse` | `Parse` | 将 SQL 解析为 AST（含 JSON） |
| 方言转译 | `POST /transpile` | `Transpile` | 在方言之间互转 SQL |
| 格式化 | `POST /format` | `Format` | 美化 / 标准化 SQL |
| 语法校验 | `POST /validate` | `Validate` | 校验 SQL 是否合法 |
| 元数据提取 | `POST /extract` | `Extract` | 提取表、列、函数、CTE |

---

## REST 调用示例

### 健康检查
```bash
curl http://localhost:8000/health
# {"status":"ok","sqlglot_version":"30.12.0"}
```

### 解析为 AST
```bash
curl -X POST http://localhost:8000/parse \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT a, b FROM t WHERE a > 1"}'
```
响应（节选）：
```json
{
  "sql": "SELECT a, b FROM t WHERE a > 1",
  "dialect": null,
  "ast": "SELECT a, b FROM t WHERE a > 1",
  "json": [ { "c": "sqlglot.expressions.query.Select" }, "..." ]
}
```

### 方言转译（MySQL → PostgreSQL）
```bash
curl -X POST http://localhost:8000/transpile \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT IFNULL(a, 0) FROM t", "read": "mysql", "write": "postgres"}'
# transpiled: SELECT COALESCE(a, 0) FROM t
```

### 格式化
```bash
curl -X POST http://localhost:8000/format \
  -H "Content-Type: application/json" \
  -d '{"sql": "select a,b from t"}'
# formatted: SELECT a, b FROM t
```

### 语法校验
```bash
curl -X POST http://localhost:8000/validate \
  -H "Content-Type: application/json" \
  -d '{"sql": "select from"}'
# {"valid": false, "error": "Expected table name ..."}
```

### 提取表名 / 列名
```bash
curl -X POST http://localhost:8000/extract \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT a, b FROM t1 JOIN t2 ON t1.id = t2.id"}'
# {"tables":["t1","t2"],"columns":["a","b","id"],"functions":[],"ctes":[]}
```

---

## gRPC 调用示例

### Python

```python
import grpc
import sqlparser_pb2 as pb
import sqlparser_pb2_grpc as pb_grpc

with grpc.insecure_channel("localhost:50051") as channel:
    stub = pb_grpc.SqlParserStub(channel)

    # 健康检查
    print(stub.Health(pb.HealthRequest()))

    # 方言转译
    r = stub.Transpile(pb.TranspileRequest(
        sql="SELECT IFNULL(a, 0) FROM t", read="mysql", write="postgres"))
    print(r.transpiled)          # SELECT COALESCE(a, 0) FROM t

    # 提取元数据
    e = stub.Extract(pb.ExtractRequest(sql="SELECT a FROM t1 JOIN t2 ON t1.id = t2.id"))
    print(list(e.tables))        # ['t1', 't2']

    # 解析 AST（json 字段为 JSON 字符串，按需反序列化）
    import json
    p = stub.Parse(pb.ParseRequest(sql="SELECT a FROM t", pretty=True))
    ast_json = json.loads(p.json)
```

> **说明**：gRPC 的 message 不便传递嵌套动态结构，因此 `Parse` 的 AST 以 JSON 字符串放在 `json` 字段返回，客户端使用 `json.loads` 解析即可。

---

## 参数说明

所有处理类接口共享以下参数：

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `sql` | string | 是 | 待处理的 SQL 语句 |
| `read` | string | 否 | 输入方言，如 `mysql` / `postgres` / `bigquery` / `hive`，留空自动识别 |
| `write` | string | 转译必填 | 目标方言（`/transpile` 必填；`/format` 可选，默认与输入一致） |
| `pretty` | bool | 否 | 仅 `/parse`：AST 是否美化输出，默认 `true` |

---

## 错误处理

| 场景 | REST | gRPC |
| --- | --- | --- |
| SQL 解析/转译失败 | HTTP `400`，body `{"detail": "解析失败: ..."}` | 状态码 `INVALID_ARGUMENT`，附错误信息 |
| 转译缺少 `write` | 请求校验失败 `422` | `INVALID_ARGUMENT`：`write(目标方言) 不能为空` |
| 语法校验（`/validate`） | HTTP `200`，返回 `{"valid": false, "error": "..."}` | 正常返回，`valid=false` |

> 注意：`/validate` 用于**主动校验**，即使 SQL 非法也返回 200；其余接口遇到非法 SQL 会返回错误状态。

---

## 支持的方言

通过 `GET /dialects` 或 gRPC `Dialects` 获取完整列表。常见方言包括：

`mysql` · `postgres` · `sqlite` · `bigquery` · `hive` · `spark` · `snowflake` · `redshift` · `presto` · `trino` · `clickhouse` · `duckdb` · `oracle` · `tsql`(SQL Server) 等。

---

## 常见问题 FAQ

**Q: 不指定 `read` 方言会怎样？**
A: sqlglot 会使用通用/默认解析器自动识别，多数标准 SQL 可正常解析。若使用了特定方言语法（如 MySQL 反引号），建议显式指定 `read`。

**Q: gRPC 客户端报 `import sqlparser_pb2` 找不到模块？**
A: 需保证 stub 文件与运行目录在同一路径，或将项目目录加入 `PYTHONPATH`。`grpc_server.py` 已自动将自身目录加入 `sys.path`。

**Q: 修改了 `.proto` 后没生效？**
A: 需重新执行 protoc 生成命令（见 [gRPC 服务](#grpc-服务)），并重启服务。

**Q: 与已安装的 gradio 存在 fastapi 版本冲突？**
A: 本服务需要较新版 fastapi。若需与 gradio 共存，可将 `requirements.txt` 中的 fastapi 固定为 `~=0.112.0` 等兼容版本。
