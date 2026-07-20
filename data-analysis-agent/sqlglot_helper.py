#!/usr/bin/env python3
"""sqlglot_helper - 使用 sqlglot 在 SQL 中注入 WHERE 子句。

从 stdin 读取 JSON: {"sql": "...", "where": "col = 'val'"}
输出 JSON: {"sql": "...", "error": ""}
"""
import json
import sys

try:
    from sqlglot import parse_one, exp
except ImportError:
    # sqlglot 未安装，输出空结果让 Go 回退
    print(json.dumps({"sql": "", "error": "sqlglot not installed"}))
    sys.exit(0)


def inject_where(sql: str, where_expr: str) -> str:
    """在 SQL 中注入 WHERE 子句，正确处理已存在的 WHERE/GROUP/ORDER/LIMIT。"""
    try:
        tree = parse_one(sql)
    except Exception as e:
        return json.dumps({"sql": "", "error": f"parse error: {e}"})

    # 解析 WHERE 表达式
    try:
        where_ast = parse_one(where_expr)
    except Exception:
        # 如果无法解析为完整表达式，尝试作为条件解析
        where_ast = None

    if where_ast is None:
        # 简单兜底：直接拼接到 WHERE 子句
        if isinstance(tree, exp.Select):
            where_clause = tree.args.get("where")
            condition = exp.Column(this=where_expr.split(" = ")[0])
            if where_clause:
                where_clause = tree.args["where"]
                where_clause.set("this", exp.And(
                    this=where_clause.args["this"],
                    expression=exp.EQ(
                        this=exp.Column(this=where_expr.split(" = ")[0]),
                        expression=exp.Literal.string(where_expr.split(" = ", 1)[1].strip("'"))
                    )
                ))
            else:
                tree.set("where", exp.Where(this=exp.EQ(
                    this=exp.Column(this=where_expr.split(" = ")[0]),
                    expression=exp.Literal.string(where_expr.split(" = ", 1)[1].strip("'"))
                )))
        return tree.sql(pretty=True)

    if not isinstance(tree, exp.Select):
        return json.dumps({"sql": "", "error": "only SELECT statements supported"})

    col = None
    val = None
    if isinstance(where_ast, exp.EQ):
        col = where_ast.left
        val = where_ast.right
    elif isinstance(where_ast, exp.Column):
        col = where_ast

    if col is None:
        return json.dumps({"sql": "", "error": f"cannot parse where expression: {where_expr}"})

    eq_condition = exp.EQ(this=col, expression=val) if val else None

    if eq_condition is None:
        return json.dumps({"sql": "", "error": "could not build EQ condition"})

    existing_where = tree.args.get("where")
    if existing_where:
        tree.set("where", exp.Where(this=exp.And(
            this=existing_where.args["this"],
            expression=eq_condition
        )))
    else:
        tree.set("where", exp.Where(this=eq_condition))

    return tree.sql(pretty=True)


def main():
    try:
        data = json.load(sys.stdin)
        sql = data.get("sql", "")
        where_expr = data.get("where", "")
        if not sql or not where_expr:
            print(json.dumps({"sql": "", "error": "missing sql or where"}))
            return
        result = inject_where(sql, where_expr)
        # result may be JSON error string or raw SQL
        try:
            parsed = json.loads(result)
            print(json.dumps(parsed))
        except json.JSONDecodeError:
            print(json.dumps({"sql": result, "error": ""}))
    except Exception as e:
        print(json.dumps({"sql": "", "error": str(e)}))


if __name__ == "__main__":
    main()
