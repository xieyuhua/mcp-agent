package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"company.com/data-analysis-agent/internal/logger"
	"company.com/data-analysis-agent/llm"
	"company.com/data-analysis-agent/mcpclient"
	"company.com/data-analysis-agent/skill"
)

// toolRunner 工具执行函数（picoclaw 风格：每个工具都是自包含的「名称+描述+处理函数」单元）。
// args 为模型已解析的参数；result 用于累积图表/数据行/SQL 等结构化结果；返回的字符串回灌给模型。
type toolRunner func(userID, convID string, args map[string]interface{}, result *AskResult, onProgress func(message string)) (string, error)

// buildRegistry 依据暴露给模型的工具清单，为每个工具绑定对应的执行函数，形成「工具名 -> 执行器」映射。
// 分发逻辑由此统一完成，executeTool 不再需要庞大的 switch。
// Agent 自身不实现任何技能，所有工具都转发到对应的 MCP 服务（主 MCP 或额外 MCP）。
func (a *Agent) buildRegistry(specs []llm.Tool) map[string]toolRunner {
	reg := make(map[string]toolRunner, len(specs))
	for _, s := range specs {
		name := s.Name
		// use_skill：加载预定义技能工作流并回灌给模型，模型随后按指引执行（与 ReAct 循环兼容）。
		if name == "use_skill" {
			reg[name] = func(userID, convID string, args map[string]interface{}, result *AskResult, onProgress func(message string)) (string, error) {
				nameArg, _ := args["name"].(string)
				a.mu.RLock()
				sk, ok := a.skills[nameArg]
				names := skill.Names(a.skills)
				a.mu.RUnlock()
				if !ok {
					return fmt.Sprintf("未找到技能 %q，可用技能: %s", nameArg, strings.Join(names, ", ")), nil
				}
				logger.Infof("[agent] 加载技能: %s", sk.Name)
				return "已加载技能「" + sk.Name + "」，请严格按以下工作流指引执行后续步骤：\n\n" + sk.Body, nil
			}
			continue
		}
		// render_chart 在 Agent 本地生成图表规格并写入结果，不转发到 MCP 后端。
		if name == "render_chart" {
			reg[name] = func(userID, convID string, args map[string]interface{}, result *AskResult, onProgress func(message string)) (string, error) {
				return renderChartLocal(args, result)
			}
			continue
		}
		// 额外对接的远程 MCP 工具：按路由表转发到对应客户端。
		if cli, ok := a.toolRoute[name]; ok {
			reg[name] = func(userID, convID string, args map[string]interface{}, result *AskResult, onProgress func(message string)) (string, error) {
				return a.callExtraMCP(userID, convID, cli, name, args, result, onProgress), nil
			}
			continue
		}
		// 其余统一走主 MCP（内置 mcp-data-server 或通用远程 MCP）。
		reg[name] = func(userID, convID string, args map[string]interface{}, result *AskResult, onProgress func(message string)) (string, error) {
			return a.callMainMCP(userID, convID, name, args, result, onProgress), nil
		}
	}
	return reg
}

// callMainMCP 调用主 MCP（内置 mcp-data-server 子进程或远程通用 MCP）。
// 内置模式会注入 token（除非 AuthDisabled）并把模型工具名映射到后端真实名；远程模式直接以原名称转发。
// dataRole 为当前用户的数据库角色，用于 Agent 侧权限校验（仅 AuthDisabled 时生效）。
func (a *Agent) callMainMCP(userID, convID, name string, args map[string]interface{}, result *AskResult, onProgress func(message string)) string {
	mcpName := name
	if a.builtin {
		mcpName = a.mcpToolName(name)
	}
	if !a.mcp.HasTool(mcpName) {
		return fmt.Sprintf("未知工具: %s", mcpName)
	}

	// Agent 侧权限校验（authz 已初始化时生效）。
	if a.builtin && a.authz != nil {
		if err := a.enforcePreQuery(mcpName, args); err != nil {
			return fmt.Sprintf("权限不足: %v", err)
		}
		// 注入数据范围 WHERE 条件（行级过滤）
		a.injectScopeFilters(mcpName, args)
	}

	toolStart := time.Now()
	text, isErr, err := a.mcp.CallTool(mcpName, args, onProgress)
	durationMs := time.Since(toolStart).Milliseconds()
	var errMsg string
	if err != nil {
		errMsg = err.Error()
	}
	a.logMCPCall(userID, convID, name, "main", marshal(args), text, durationMs, isErr, errMsg)
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
	// Agent 侧结果过滤：脱敏 + 隐藏字段（masker 已初始化时生效）。
	if a.builtin && a.masker != nil {
		text = a.enforcePostQuery(mcpName, args, text, result)
	}
	// 从 MCP 返回结果中提取图表规格（若某 MCP 工具返回了含 chart 字段的 JSON）。
	extractChart(text, result)
	if rows := parseRows(text); rows != nil {
		result.Rows = rows
	}
	// 注意：返回完整结果（前端「分析过程」不截断）；仅当把结果回灌给模型时才在调用处截断。
	return text
}

// enforcePreQuery 在调用 MCP 前校验权限。
func (a *Agent) enforcePreQuery(toolName string, args map[string]interface{}) error {
	dataRole := "super_admin" // 默认使用 admin 角色；HTTP 服务模式下由前端传入覆盖
	if r, ok := args["_data_role"].(string); ok && r != "" {
		dataRole = r
		delete(args, "_data_role")
	}
	tenantID := ""

	switch toolName {
	case "query_table":
		table, _ := args["table"].(string)
		if table == "" {
			return fmt.Errorf("table is required")
		}
		if !a.authz.AllowedTables(tenantID, dataRole)[table] {
			return fmt.Errorf("角色 %q 无权访问表 %q", dataRole, table)
		}
		// 字段级权限检查
		hidden := a.authz.HiddenFields(tenantID, dataRole)[table]
		if fields, ok := args["fields"].([]interface{}); ok {
			for _, f := range fields {
				if s, ok := f.(string); ok && hidden[s] {
					return fmt.Errorf("字段 %q 对角色 %q 隐藏", s, dataRole)
				}
			}
		}
	case "run_sql":
		if !a.authz.CanRunRawSQL(tenantID, dataRole) {
			return fmt.Errorf("角色 %q 不允许执行原生 SQL", dataRole)
		}
	case "describe_table":
		table, _ := args["table"].(string)
		if table == "" {
			return fmt.Errorf("table is required")
		}
		if !a.authz.AllowedTables(tenantID, dataRole)[table] {
			return fmt.Errorf("角色 %q 无权访问表 %q", dataRole, table)
		}
	}
	return nil
}

// enforcePostQuery 在 MCP 返回结果后过滤隐藏字段并脱敏。
func (a *Agent) enforcePostQuery(toolName string, args map[string]interface{}, text string, result *AskResult) string {
	dataRole := "super_admin"
	if r, ok := args["_data_role"].(string); ok && r != "" {
		dataRole = r
	}
	tenantID := ""

	switch toolName {
	case "query_table":
		table, _ := args["table"].(string)
		if table == "" || text == "" {
			return text
		}
		hidden := a.authz.HiddenFields(tenantID, dataRole)[table]
		if len(hidden) == 0 && a.masker.Rules(tenantID) == nil {
			return text
		}
		var rows []map[string]interface{}
		if err := json.Unmarshal([]byte(text), &rows); err != nil || len(rows) == 0 {
			return text
		}
		for i, row := range rows {
			// 脱敏
			if a.masker != nil {
				row = a.masker.MaskRow(tenantID, table, row)
			}
			// 隐藏字段
			for col := range hidden {
				delete(row, col)
			}
			rows[i] = row
		}
		b, _ := json.Marshal(rows)
		result.Rows = rows
		return string(b)
	case "run_sql":
		// 从 SQL 中提取涉及的表名，合并隐藏字段
		table := extractFirstTableFromSQL(args)
		if table == "" {
			return text
		}
		hidden := a.authz.HiddenFields(tenantID, dataRole)[table]
		if len(hidden) == 0 {
			return text
		}
		var rows []map[string]interface{}
		if err := json.Unmarshal([]byte(text), &rows); err != nil || len(rows) == 0 {
			return text
		}
		for i, row := range rows {
			for col := range hidden {
				delete(row, col)
			}
			rows[i] = row
		}
		b, _ := json.Marshal(rows)
		result.Rows = rows
		return string(b)
	case "describe_table":
		table, _ := args["table"].(string)
		if table == "" || text == "" {
			return text
		}
		hidden := a.authz.HiddenFields(tenantID, dataRole)[table]
		if len(hidden) == 0 {
			return text
		}
		// describe_table 返回 {"table":"...","columns":[...]}
		var desc struct {
			Table   string   `json:"table"`
			Columns []string `json:"columns"`
		}
		if err := json.Unmarshal([]byte(text), &desc); err != nil || len(desc.Columns) == 0 {
			return text
		}
		filtered := make([]string, 0, len(desc.Columns))
		for _, col := range desc.Columns {
			if !hidden[col] {
				filtered = append(filtered, col)
			}
		}
		desc.Columns = filtered
		b, _ := json.Marshal(desc)
		return string(b)
	}
	return text
}

// injectScopeFilters 根据用户数据角色范围，向工具参数注入行级 WHERE 条件。
// query_table：向 filters 追加等值条件；run_sql：通过 sqlglot 在 SQL 中注入 WHERE 子句。
func (a *Agent) injectScopeFilters(toolName string, args map[string]interface{}) {
	dataRole := "super_admin"
	if r, ok := args["_data_role"].(string); ok && r != "" {
		dataRole = r
	}
	tenantID := ""
	userIDs := map[string]string{
		"tenant_id": "",
		"region_id": "",
		"store_id":  "",
	}
	for k := range userIDs {
		if s, ok := args["_"+k].(string); ok {
			userIDs[k] = s
		}
	}

	f := a.authz.ScopeFilter(tenantID, dataRole, userIDs)
	if len(f) == 0 {
		return
	}

	switch toolName {
	case "query_table":
		existing, _ := args["filters"].(map[string]interface{})
		if existing == nil {
			existing = make(map[string]interface{})
		}
		for col, val := range f {
			existing[col] = val
		}
		args["filters"] = existing

	case "run_sql":
		sql, _ := args["sql"].(string)
		if sql == "" {
			return
		}
		expr := a.authz.ScopeFilterExpr(tenantID, dataRole, userIDs)
		if expr == "" {
			return
		}
		modified, err := injectWhereWithSqlglot(sql, expr)
		if err != nil || modified == "" {
			// sqlglot 不可用时，尝试简单追加（已有 WHERE 则 AND，否则追加 WHERE）
			upper := strings.ToUpper(strings.TrimSpace(sql))
			if strings.Contains(upper, "WHERE ") {
				args["sql"] = sql + " AND " + expr
			} else {
				// 在 ORDER/GROUP/LIMIT 前插入
				insertPos := len(sql)
				for _, kw := range []string{" ORDER ", " GROUP ", " LIMIT ", " HAVING "} {
					idx := strings.Index(upper, kw)
					if idx > 0 && idx < insertPos {
						insertPos = idx
					}
				}
				args["sql"] = sql[:insertPos] + " WHERE " + expr + sql[insertPos:]
			}
		} else {
			args["sql"] = modified
		}
	}
}

// injectWhereWithSqlglot 调用 Python sqlglot 工具在 SQL 中注入 WHERE 子句。
// 返回修改后的 SQL；若 Python 执行失败则返回空字符串。
func injectWhereWithSqlglot(sql, whereExpr string) (string, error) {
	// 尝试调用 Python sqlglot 脚本
	// 脚本路径：agent 同目录下的 sqlglot_helper.py
	scriptPath := "sqlglot_helper.py"
	
	// 检查文件是否存在；若不存在则回退到 Go 简单实现
	if _, err := os.Stat(scriptPath); err != nil {
		return "", nil // 无脚本则回退
	}

	input := map[string]string{"sql": sql, "where": whereExpr}
	inputJSON, _ := json.Marshal(input)

	cmd := exec.Command("python3", scriptPath)
	cmd.Stdin = strings.NewReader(string(inputJSON))
	output, err := cmd.Output()
	if err != nil {
		logger.Warnf("[sqlglot] 执行失败: %v", err)
		return "", err
	}

	var result struct {
		SQL    string `json:"sql"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("%s", result.Error)
	}
	if result.SQL != "" {
		return result.SQL, nil
	}
	return "", nil
}

// extractFirstTableFromSQL 从 run_sql 参数中提取表名（用于隐藏字段过滤）。
func extractFirstTableFromSQL(args map[string]interface{}) string {
	sql, _ := args["sql"].(string)
	if sql == "" {
		sql, _ = args["query"].(string)
	}
	// 简单提取 FROM/JOIN 后的表名
	idx := strings.Index(strings.ToUpper(sql), "FROM ")
	if idx < 0 {
		idx = strings.Index(strings.ToUpper(sql), "JOIN ")
	}
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(sql[idx+5:])
	space := strings.Index(rest, " ")
	if space > 0 {
		return rest[:space]
	}
	return rest
}

// callExtraMCP 调用额外对接的远程 MCP 服务（通用 MCP，不注入 token）。
func (a *Agent) callExtraMCP(userID, convID string, cli *mcpclient.Client, name string, args map[string]interface{}, result *AskResult, onProgress func(message string)) string {
	toolStart := time.Now()
	text, isErr, err := cli.CallTool(name, args, onProgress)
	durationMs := time.Since(toolStart).Milliseconds()
	var errMsg string
	if err != nil {
		errMsg = err.Error()
	}
	a.logMCPCall(userID, convID, name, "extra", marshal(args), text, durationMs, isErr, errMsg)
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
	// 返回完整结果（前端「分析过程」不截断）。
	return text
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
