"""
gRPC 客户端调用示例

先启动服务:
    python grpc_server.py
再运行:
    python grpc_client_example.py
"""

from __future__ import annotations

import grpc

import sqlparser_pb2 as pb
import sqlparser_pb2_grpc as pb_grpc


def main(target: str = "localhost:50051") -> None:
    with grpc.insecure_channel(target) as channel:
        stub = pb_grpc.SqlParserStub(channel)

        print("Health:", stub.Health(pb.HealthRequest()))

        print("Dialects 数量:", len(stub.Dialects(pb.DialectsRequest()).dialects))

        r = stub.Transpile(pb.TranspileRequest(sql="SELECT IFNULL(a, 0) FROM t", read="mysql", write="postgres"))
        print("Transpile:", r.transpiled)

        r = stub.Format(pb.FormatRequest(sql="select a,b from t"))
        print("Format:", r.formatted)

        r = stub.Validate(pb.ValidateRequest(sql="select from"))
        print("Validate(非法):", r.valid, r.error[:40])

        r = stub.Extract(pb.ExtractRequest(sql="SELECT a FROM t1 JOIN t2 ON t1.id = t2.id"))
        print("Extract tables:", list(r.tables), "columns:", list(r.columns))


if __name__ == "__main__":
    main()
