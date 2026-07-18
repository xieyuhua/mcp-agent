package agent

import (
	"encoding/json"
	"fmt"

	"company.com/data-analysis-agent/llm"
	"company.com/data-analysis-agent/mcpclient"
)

// toolRunner 工具执行函数（picoclaw 风格：每个工具都是自包含的「名称+描述+处理函数」单元）。
// args 为模型已解析的参数；result 用于累积图表/数据行/SQL 等结构化结果；返回的字符串回灌给模型。
type toolRunner func(args map[string]interface{}, result *AskResult) (string, error)

// buildRegistry 依据暴露给模型的工具清单，为每个工具绑定对应的执行函数，形成「工具名 -> 执行器」映射。
// 分发逻辑由此统一完成，executeTool 不再需要庞大的 switch。
// Agent 自身不实现任何技能，所有工具都转发到对应的 MCP 服务（主 MCP 或额外 MCP）。
func (a *Agent) buildRegistry(specs []llm.Tool) map[string]toolRunner {
	reg := make(map[string]toolRunner, len(specs))
	for _, s := range specs {
		name := s.Name
		// 额外对接的远程 MCP 工具：按路由表转发到对应客户端。
		if cli, ok := a.toolRoute[name]; ok {
			reg[name] = func(args map[string]interface{}, result *AskResult) (string, error) {
				return a.callExtraMCP(cli, name, args, result), nil
			}
			continue
		}
		// 其余统一走主 MCP（内置 mcp-data-server 或通用远程 MCP）。
		reg[name] = func(args map[string]interface{}, result *AskResult) (string, error) {
			return a.callMainMCP(name, args, result), nil
		}
	}
	return reg
}

// callMainMCP 调用主 MCP（内置 mcp-data-server 子进程或远程通用 MCP）。
// 内置模式会注入 token 并把模型工具名映射到后端真实名；远程模式直接以原名称转发。
func (a *Agent) callMainMCP(name string, args map[string]interface{}, result *AskResult) string {
	mcpName := name
	if a.builtin {
		args["token"] = a.token
		mcpName = a.mcpToolName(name)
	}
	if !a.mcp.HasTool(mcpName) {
		return fmt.Sprintf("未知工具: %s", mcpName)
	}
	text, isErr, err := a.mcp.CallTool(mcpName, args)
	if err != nil {
		return fmt.Sprintf("工具调用出错: %v", err)
	}
	if isErr {
		return "工具执行失败: " + text
	}
	// 记录 SQL 与返回的数据行，供前端表格/图表兜底展示（仅内置模式有 run_sql）。
	if a.builtin && name == "run_sql" {
		if sql, ok := args["sql"].(string); ok {
			result.SQL = sql
		}
	}
	// 从 MCP 返回结果中提取图表规格（若某 MCP 工具返回了含 chart 字段的 JSON）。
	extractChart(text, result)
	if rows := parseRows(text); rows != nil {
		result.Rows = rows
	}
	return a.truncateResult(text)
}

// callExtraMCP 调用额外对接的远程 MCP 服务（通用 MCP，不注入 token）。
func (a *Agent) callExtraMCP(cli *mcpclient.Client, name string, args map[string]interface{}, result *AskResult) string {
	text, isErr, err := cli.CallTool(name, args)
	if err != nil {
		return fmt.Sprintf("工具调用出错: %v", err)
	}
	if isErr {
		return "工具执行失败: " + text
	}
	// 同样尝试从返回结果中提取图表规格。
	extractChart(text, result)
	if rows := parseRows(text); rows != nil {
		result.Rows = rows
	}
	return a.truncateResult(text)
}

// extractChart 若 MCP 返回文本是含 chart 字段的 JSON，则解析为 ChartSpec 存入结果。
// 这样图表由提供该能力的 MCP 服务端产出，Agent 只负责透传提取，不实现绘图技能。
func extractChart(text string, result *AskResult) {
	if result.Chart != nil {
		return // 已存在则不覆盖
	}
	var wrapper struct {
		Chart *ChartSpec `json:"chart"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err != nil || wrapper.Chart == nil {
		return
	}
	spec := wrapper.Chart
	if spec.Type == "" {
		spec.Type = "bar"
	}
	if len(spec.Series) == 0 || len(spec.Categories) == 0 {
		return // 图表数据不完整，忽略
	}
	result.Chart = spec
}
