"""
SQL 解析 API 服务 —— 基于 sqlglot

提供以下能力:
  1. /parse      将 SQL 解析为结构化 AST(JSON)
  2. /transpile  在方言之间互转 SQL(如 MySQL -> PostgreSQL)
  3. /format     美化 / 格式化 SQL
  4. /validate   校验 SQL 语法是否合法
  5. /extract    提取 SQL 中的表名、列名、函数调用等信息
  6. /dialects   列出 sqlglot 支持的所有方言

运行:
    pip install -r requirements.txt
    uvicorn main:app --host 0.0.0.0 --port 8000 --reload

交互式文档: http://localhost:8000/docs
"""

from __future__ import annotations

import sqlglot
from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel, Field

app = FastAPI(
    title="SQL 解析 API",
    description="基于 sqlglot 的 SQL 解析 / 转译 / 格式化 / 提取服务",
    version="1.0.0",
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)


# --------------------------------------------------------------------------- #
# 请求 / 响应模型
# --------------------------------------------------------------------------- #
class ParseRequest(BaseModel):
    sql: str = Field(..., description="待解析的 SQL 语句", examples=["SELECT a, b FROM t WHERE a > 1"])
    read: str | None = Field(None, description="输入 SQL 的方言, 如 mysql / postgres / bigquery, 留空自动识别")
    pretty: bool = Field(True, description="AST 是否美化输出")


class TranspileRequest(BaseModel):
    sql: str = Field(..., description="待转译的 SQL 语句")
    read: str | None = Field(None, description="输入方言, 留空自动识别")
    write: str = Field(..., description="目标方言, 如 postgres / mysql / sqlite / bigquery")


class FormatRequest(BaseModel):
    sql: str = Field(..., description="待格式化的 SQL 语句")
    read: str | None = Field(None, description="输入方言, 留空自动识别")
    write: str | None = Field(None, description="输出方言, 默认与输入一致")


class ValidateRequest(BaseModel):
    sql: str = Field(..., description="待校验的 SQL 语句")
    read: str | None = Field(None, description="输入方言, 留空自动识别")


class ExtractRequest(BaseModel):
    sql: str = Field(..., description="待提取信息的 SQL 语句")
    read: str | None = Field(None, description="输入方言, 留空自动识别")


# --------------------------------------------------------------------------- #
# 工具函数
# --------------------------------------------------------------------------- #
def _resolve_dialect(dialect: str | None) -> str | None:
    """校验方言是否受支持, 返回规范后的方言名(大写)."""
    if not dialect:
        return None
    try:
        # sqlglot.Dialects 在较新版本提供; 旧版本用 str 即可
        return sqlglot.Dialect.get_or_raise(dialect).get_name() if hasattr(sqlglot.Dialect, "get_or_raise") else dialect
    except Exception:
        return dialect


# --------------------------------------------------------------------------- #
# 接口
# --------------------------------------------------------------------------- #
@app.get("/health")
def health() -> dict:
    return {"status": "ok", "sqlglot_version": sqlglot.__version__}


@app.get("/dialects")
def dialects() -> dict:
    """列出所有受支持的方言."""
    try:
        names = [d for d in sqlglot.Dialect.classes() if d]
    except Exception:
        names = list(sqlglot.dialects.Dialects.__members__.keys()) if hasattr(sqlglot, "dialects") else []
    return {"dialects": sorted(names)}


@app.post("/parse")
def parse(req: ParseRequest) -> dict:
    try:
        expression = sqlglot.parse_one(req.sql, read=req.read)
    except Exception as exc:  # noqa: BLE001
        raise HTTPException(status_code=400, detail=f"解析失败: {exc}") from exc
    return {
        "sql": req.sql,
        "dialect": req.read,
        "ast": expression.sql(pretty=False) if req.pretty else str(expression),
        "tree": expression.repr() if hasattr(expression, "repr") else None,
        "json": expression.dump() if hasattr(expression, "dump") else None,
    }


@app.post("/transpile")
def transpile(req: TranspileRequest) -> dict:
    try:
        out = sqlglot.transpile(req.sql, read=req.read, write=req.write)[0]
    except Exception as exc:  # noqa: BLE001
        raise HTTPException(status_code=400, detail=f"转译失败: {exc}") from exc
    return {"read": req.read, "write": req.write, "sql": req.sql, "transpiled": out}


@app.post("/format")
def format_sql(req: FormatRequest) -> dict:
    try:
        out = sqlglot.transpile(req.sql, read=req.read, write=req.write or req.read)[0]
    except Exception as exc:  # noqa: BLE001
        raise HTTPException(status_code=400, detail=f"格式化失败: {exc}") from exc
    return {"sql": req.sql, "formatted": out}


@app.post("/validate")
def validate(req: ValidateRequest) -> dict:
    try:
        sqlglot.parse_one(req.sql, read=req.read)
    except Exception as exc:  # noqa: BLE001
        return {"valid": False, "error": str(exc)}
    return {"valid": True, "error": None}


@app.post("/extract")
def extract(req: ExtractRequest) -> dict:
    try:
        expression = sqlglot.parse_one(req.sql, read=req.read)
    except Exception as exc:  # noqa: BLE001
        raise HTTPException(status_code=400, detail=f"解析失败: {exc}") from exc

    tables, columns, functions, ctes = set(), set(), set(), set()

    for node in expression.walk():
        t = type(node).__name__
        # 表名
        if t in ("Table", "Schema"):
            if hasattr(node, "this") and getattr(node, "this") is not None:
                name = getattr(node.this, "this", None)
                if name is not None:
                    tables.add(str(name))
        # 列引用
        if t == "Column":
            col = getattr(node, "this", None)
            if col is not None:
                columns.add(str(col))
        # 函数调用
        if t == "Func":
            functions.add(node.sql(req.read))
        # CTE
        if t == "CTE":
            alias = getattr(node, "alias", None)
            if alias is not None:
                ctes.add(str(alias))

    return {
        "tables": sorted(tables),
        "columns": sorted(columns),
        "functions": sorted(functions),
        "ctes": sorted(ctes),
    }


if __name__ == "__main__":
    import uvicorn

    uvicorn.run("main:app", host="0.0.0.0", port=8000, reload=True)
