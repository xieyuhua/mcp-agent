@echo off
REM ============================================================
REM  一键启动：mcp-data-server(HTTP :9000) + data-analysis-agent(remote 对接 :8088)
REM  前置依赖：Go 1.22+、Ollama(本地模型 qwen3:8b)
REM  用法：双击 start.bat 或 cmd 中执行；两个服务各占一个窗口，按任意键统一停止
REM ============================================================
setlocal
cd /d %~dp0

echo [build] 编译 mcp-data-server ...
cd mcp-data-server
go build -o main.exe ./cmd/server
if errorlevel 1 ( echo 编译 mcp-data-server 失败 & exit /b 1 )

echo [build] 编译 data-analysis-agent ...
cd ../data-analysis-agent
go build -o data-analysis-agent.exe .
if errorlevel 1 ( echo 编译 data-analysis-agent 失败 & exit /b 1 )

echo [1/2] 启动 mcp-data-server (HTTP :9000)  权限后台: http://localhost:9000
start "mcp-data-server" cmd /c "cd /d %~dp0mcp-data-server && main.exe -config config.http.json"
timeout /t 3 /nobreak > nul

echo [2/2] 启动 data-analysis-agent (remote 对接 :8088)  API: http://localhost:8088
start "data-analysis-agent" cmd /c "cd /d %~dp0data-analysis-agent && data-analysis-agent.exe -serve -addr :8088 -config config.remote.json"

echo.
echo 两个服务已启动（日志见上方两个窗口）：
echo   - Agent API:      http://localhost:8088
echo   - MCP 权限后台:   http://localhost:9000   (admin / admin123)
echo 按任意键停止所有服务...
pause > nul

taskkill /IM main.exe /F 2>nul
taskkill /IM data-analysis-agent.exe /F 2>nul
echo 已停止。

endlocal
