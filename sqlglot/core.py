"""
SQL 解析核心逻辑 —— 基于 sqlglot

被 REST(main.py) 与 gRPC(grpc_server.py) 两种服务共享复用。
所有函数在失败时抛出 SqlError, 由上层适配为对应协议的错误响应。
"""

from __future__ import annotations

import sqlglot


class SqlError(Exception):
    """SQL 处理过程中的业务异常."""


def sqlglot_version() -> str:
    return sqlglot.__version__


def list_dialects() -> list[str]:
    """列出所有受支持的方言."""
    try:
        names = [d for d in sqlglot.Dialect.classes() if d]
    except Exception:
        names = list(sqlglot.dialects.Dialects.__members__.keys()) if hasattr(sqlglot, "dialects") else []
    return sorted(names)


def parse(sql: str, read: str | None = None, pretty: bool = True) -> dict:
    """将 SQL 解析为结构化 AST."""
    try:
        expression = sqlglot.parse_one(sql, read=read)
    except Exception as exc:  # noqa: BLE001
        raise SqlError(f"解析失败: {exc}") from exc
    return {
        "sql": sql,
        "dialect": read,
        "ast": expression.sql(pretty=False) if pretty else str(expression),
        "json": expression.dump() if hasattr(expression, "dump") else None,
    }


def transpile(sql: str, write: str, read: str | None = None) -> str:
    """在方言之间互转 SQL."""
    try:
        return sqlglot.transpile(sql, read=read, write=write)[0]
    except Exception as exc:  # noqa: BLE001
        raise SqlError(f"转译失败: {exc}") from exc


def format_sql(sql: str, read: str | None = None, write: str | None = None) -> str:
    """美化 / 格式化 SQL."""
    try:
        return sqlglot.transpile(sql, read=read, write=write or read)[0]
    except Exception as exc:  # noqa: BLE001
        raise SqlError(f"格式化失败: {exc}") from exc


def validate(sql: str, read: str | None = None) -> tuple[bool, str | None]:
    """校验 SQL 语法是否合法, 返回 (是否合法, 错误信息)."""
    try:
        sqlglot.parse_one(sql, read=read)
    except Exception as exc:  # noqa: BLE001
        return False, str(exc)
    return True, None


def extract(sql: str, read: str | None = None) -> dict:
    """提取 SQL 中的表名、列名、函数、CTE."""
    try:
        expression = sqlglot.parse_one(sql, read=read)
    except Exception as exc:  # noqa: BLE001
        raise SqlError(f"解析失败: {exc}") from exc

    tables, columns, functions, ctes = set(), set(), set(), set()

    for node in expression.walk():
        t = type(node).__name__
        if t in ("Table", "Schema"):
            if getattr(node, "this", None) is not None:
                name = getattr(node.this, "this", None)
                if name is not None:
                    tables.add(str(name))
        if t == "Column":
            col = getattr(node, "this", None)
            if col is not None:
                columns.add(str(col))
        if t == "Func":
            functions.add(node.sql(read))
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
