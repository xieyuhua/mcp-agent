#!/bin/bash
# ============================================================
#  一键启动：mcp-data-server(HTTP :9000) + data-analysis-agent(remote 对接 :8088)
#  前置依赖：Go 1.22+、Ollama(本地模型 qwen3:8b)
#  用法：chmod +x start.sh && ./start.sh
# ============================================================
set -e
cd "$(dirname "$0")"

echo "[build] 编译 mcp-data-server ..."
( cd mcp-data-server && go build -o main ./cmd/server )
echo "[build] 编译 data-analysis-agent ..."
( cd data-analysis-agent && go build -o data-analysis-agent . )

echo "[1/2] 启动 mcp-data-server (HTTP :9000)  权限后台: http://localhost:9000"
( cd mcp-data-server && ./main -config config.http.json ) &
MCP_PID=$!
sleep 3

echo "[2/2] 启动 data-analysis-agent (remote 对接 :8088)  API: http://localhost:8088"
( cd data-analysis-agent && ./data-analysis-agent -serve -addr :8088 -config config.remote.json )

kill $MCP_PID 2>/dev/null || true
