# SQL 解析 API 服务

基于 [sqlglot](https://github.com/tobymao/sqlglot) + FastAPI 的 SQL 解析服务，提供解析、转译、格式化、校验与元数据提取能力。

## 功能

| 接口 | 方法 | 说明 |
| --- | --- | --- |
| `/health` | GET | 健康检查，返回 sqlglot 版本 |
| `/dialects` | GET | 列出所有支持的方言 |
| `/parse` | POST | 将 SQL 解析为结构化 AST（JSON） |
| `/transpile` | POST | 在方言之间互转 SQL（如 MySQL → PostgreSQL） |
| `/format` | POST | 美化 / 格式化 SQL |
| `/validate` | POST | 校验 SQL 语法是否合法 |
| `/extract` | POST | 提取表名、列名、函数、CTE |

## 快速开始

```bash
pip install -r requirements.txt
uvicorn main:app --host 0.0.0.0 --port 8000 --reload
```

启动后访问交互式文档：<http://localhost:8000/docs>

## 调用示例

### 解析为 AST
```bash
curl -X POST http://localhost:8000/parse \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT a, b FROM t WHERE a > 1"}'
```

### 方言转译（MySQL → PostgreSQL）
```bash
curl -X POST http://localhost:8000/transpile \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT * FROM t LIMIT 10", "read": "mysql", "write": "postgres"}'
```

### 提取表名 / 列名
```bash
curl -X POST http://localhost:8000/extract \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT a, b FROM t1 JOIN t2 ON t1.id = t2.id"}'
```

## 请求参数说明

- `sql`（必填）：待处理的 SQL 语句
- `read`（可选）：输入方言，如 `mysql` / `postgres` / `bigquery` / `hive`，留空自动识别
- `write`（转译必填）：目标方言
- `pretty`（解析可选）：AST 是否美化输出
