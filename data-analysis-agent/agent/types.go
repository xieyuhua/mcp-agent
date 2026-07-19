package agent

// ChartSeries 图表的一个数据系列。
type ChartSeries struct {
	Name string    `json:"name"`
	Data []float64 `json:"data"`
}

// ChartSpec 由大模型通过 render_chart 工具产出的图表规格，前端用 ECharts 渲染。
type ChartSpec struct {
	// Type 图表类型：bar | line | pie。
	Type string `json:"type"`
	// Title 图表标题。
	Title string `json:"title"`
	// Categories X 轴分类（柱状/折线）或饼图各扇区标签。
	Categories []string `json:"categories"`
	// Series 数据系列。饼图只取第一个系列。
	Series []ChartSeries `json:"series"`
}

// StepLog 一次工具调用的执行痕迹，便于前端展示"思考过程"。
type StepLog struct {
	ID       string `json:"id,omitempty"`        // 工具调用 ID（对应 LLM 的 tool_call_id），并发执行时前端据此归类到独立卡片，避免输出混乱
	Tool     string `json:"tool"`
	Args     string `json:"args"`
	Result   string `json:"result"`
	Progress string `json:"progress,omitempty"` // 工具执行期间的流式进度提示（如「已读取 1200 行」）
	Duration int64  `json:"duration,omitempty"`   // 工具执行耗时（毫秒）
}

// AskResult 一次提问的结构化结果，供 HTTP 接口返回给前端。
type AskResult struct {
	// Answer 大模型给出的最终文字分析结论。
	Answer string `json:"answer"`
	// Chart 若模型调用了 render_chart，则包含图表规格；否则为 nil。
	Chart *ChartSpec `json:"chart,omitempty"`
	// Rows 最近一次数据查询返回的行（用于前端表格展示）。
	Rows []map[string]interface{} `json:"rows,omitempty"`
	// SQL 最近一次执行的原生 SQL（若有）。
	SQL string `json:"sql,omitempty"`
	// Steps 工具调用轨迹。
	Steps []StepLog `json:"steps,omitempty"`
	// TotalDuration 整轮请求总耗时（毫秒）。
	TotalDuration int64 `json:"total_duration,omitempty"`
	// LLMDuration 模型调用累计耗时（毫秒）。
	LLMDuration int64 `json:"llm_duration,omitempty"`
	// ToolDuration 工具调用累计耗时（毫秒）。
	ToolDuration int64 `json:"tool_duration,omitempty"`
}
