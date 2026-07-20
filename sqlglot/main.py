"""
SQL 解析 API 服务(REST) —— 基于 sqlglot

提供以下能力:
  1. /parse      将 SQL 解析为结构化 AST(JSON)
  2. /transpile  在方言之间互转 SQL(如 MySQL -> PostgreSQL)
  3. /format     美化 / 格式化 SQL
  4. /validate   校验 SQL 语法是否合法
  5. /extract    提取 SQL 中的表名、列名、函数调用等信息
  6. /dialects   列出 sqlglot 支持的所有方言

核心逻辑位于 core.py, 与 gRPC 服务(grpc_server.py) 共享复用。

运行:
    pip install -r requirements.txt
    uvicorn main:app --host 0.0.0.0 --port 8000 --reload

交互式文档: http://localhost:8000/docs
"""

from __future__ import annotations

from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel, Field

import core

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
# 请求模型
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
# 接口
# --------------------------------------------------------------------------- #
@app.get("/health")
def health() -> dict:
    return {"status": "ok", "sqlglot_version": core.sqlglot_version()}


@app.get("/dialects")
def dialects() -> dict:
    return {"dialects": core.list_dialects()}


@app.post("/parse")
def parse(req: ParseRequest) -> dict:
    try:
        return core.parse(req.sql, read=req.read, pretty=req.pretty)
    except core.SqlError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc


@app.post("/transpile")
def transpile(req: TranspileRequest) -> dict:
    try:
        out = core.transpile(req.sql, write=req.write, read=req.read)
    except core.SqlError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    return {"read": req.read, "write": req.write, "sql": req.sql, "transpiled": out}


@app.post("/format")
def format_sql(req: FormatRequest) -> dict:
    try:
        out = core.format_sql(req.sql, read=req.read, write=req.write)
    except core.SqlError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    return {"sql": req.sql, "formatted": out}


@app.post("/validate")
def validate(req: ValidateRequest) -> dict:
    valid, error = core.validate(req.sql, read=req.read)
    return {"valid": valid, "error": error}


@app.post("/extract")
def extract(req: ExtractRequest) -> dict:
    try:
        return core.extract(req.sql, read=req.read)
    except core.SqlError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc


if __name__ == "__main__":
    import uvicorn

    uvicorn.run("main:app", host="0.0.0.0", port=8000, reload=True)
