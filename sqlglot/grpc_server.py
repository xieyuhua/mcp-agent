"""
SQL 解析 gRPC 服务端 —— 基于 sqlglot

依赖由 sqlparser.proto 生成的 stub:
    sqlparser_pb2.py / sqlparser_pb2_grpc.py

重新生成 stub:
    python -m grpc_tools.protoc -I. --python_out=. --grpc_python_out=. sqlparser.proto

运行:
    python grpc_server.py        # 默认监听 0.0.0.0:50051
"""

from __future__ import annotations

import json
import os
import sys
from concurrent import futures

import grpc

# 确保能 import 到同目录下生成的 stub
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import core
import sqlparser_pb2 as pb
import sqlparser_pb2_grpc as pb_grpc


class SqlParserServicer(pb_grpc.SqlParserServicer):
    def Health(self, request, context):
        return pb.HealthReply(status="ok", sqlglot_version=core.sqlglot_version())

    def Dialects(self, request, context):
        return pb.DialectsReply(dialects=core.list_dialects())

    def Parse(self, request, context):
        try:
            result = core.parse(request.sql, read=request.read or None, pretty=request.pretty)
        except core.SqlError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        return pb.ParseReply(
            sql=result["sql"],
            dialect=result["dialect"] or "",
            ast=result["ast"] or "",
            json=json.dumps(result["json"], ensure_ascii=False) if result["json"] is not None else "",
        )

    def Transpile(self, request, context):
        if not request.write:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "write(目标方言) 不能为空")
        try:
            out = core.transpile(request.sql, write=request.write, read=request.read or None)
        except core.SqlError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        return pb.TranspileReply(read=request.read or "", write=request.write, sql=request.sql, transpiled=out)

    def Format(self, request, context):
        try:
            out = core.format_sql(request.sql, read=request.read or None, write=request.write or None)
        except core.SqlError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        return pb.FormatReply(sql=request.sql, formatted=out)

    def Validate(self, request, context):
        valid, error = core.validate(request.sql, read=request.read or None)
        return pb.ValidateReply(valid=valid, error=error or "")

    def Extract(self, request, context):
        try:
            result = core.extract(request.sql, read=request.read or None)
        except core.SqlError as exc:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
        return pb.ExtractReply(
            tables=result["tables"],
            columns=result["columns"],
            functions=result["functions"],
            ctes=result["ctes"],
        )


def serve(host: str = "0.0.0.0", port: int = 50051, max_workers: int = 10) -> None:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=max_workers))
    pb_grpc.add_SqlParserServicer_to_server(SqlParserServicer(), server)
    server.add_insecure_port(f"{host}:{port}")
    server.start()
    print(f"gRPC SqlParser 服务已启动: {host}:{port}")
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
