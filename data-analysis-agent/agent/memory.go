package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"company.com/data-analysis-agent/llm"
)

// HistoryItem 一条带结构化结果的历史消息（由 HTTP 层从库的 extra 字段解析而来）。
// 用于多轮对话记忆：assistant 的历史不仅包含文字，还包含图表/表格/SQL 等结果，
// 回放时一并带回到模型上下文，避免跨轮引用数据失真。
type HistoryItem struct {
	Role    string // user | assistant
	Content string
	// Extra 仅 assistant 有：图表/表格/SQL/步骤的 JSON（agent.marshalExtra 格式）。
	Extra string
}

// memoryConfig 记忆层参数。
type memoryConfig struct {
	// SummaryThreshold 历史消息数达到该值时，对早期消息做摘要压缩。
	SummaryThreshold int
	// RecentKeep 摘要压缩时，保留最近 N 条原文（其余早期消息被压缩为 summary）。
	RecentKeep int
	// MaxHistory 单次最多回放的历史条数（超出仅取最近部分，防止上下文爆炸）。
	MaxHistory int
}

// defaultMemoryConfig 默认记忆参数（兜底，配置缺失时使用）。
func defaultMemoryConfig() memoryConfig {
	return memoryConfig{SummaryThreshold: 12, RecentKeep: 6, MaxHistory: 30}
}

// buildMemoryContext 把历史消息组织成模型可读的上下文。
// 返回：
//   - summary：早期对话的压缩摘要（可能为空）
//   - messages：按时间正序的历史消息（已注入结构化结果回放）
//
// 策略（picoclaw 风格轻量记忆）：
//  1. 超出 MaxHistory 时仅保留最近部分。
//  2. 消息数达到 SummaryThreshold 时，对「除最近 RecentKeep 条之外」的早期消息用 LLM 压缩为 summary，
//     注入 system 提示；近 RecentKeep 条保留原文 + 结构化结果回放。
//  3. 未达阈值时，全部保留原文 + 结构化结果回放。
func (a *Agent) buildMemoryContext(history []HistoryItem, mc memoryConfig, llmClient *llm.Client) (summary string, messages []llm.Message) {
	if len(history) == 0 {
		return "", nil
	}
	// 1) 截断到最近 MaxHistory 条
	if mc.MaxHistory > 0 && len(history) > mc.MaxHistory {
		history = history[len(history)-mc.MaxHistory:]
	}

	// 2) 是否触发摘要压缩
	if mc.SummaryThreshold > 0 && len(history) > mc.SummaryThreshold {
		split := len(history) - mc.RecentKeep
		if split < 1 {
			split = 1
		}
		early := history[:split]
		recent := history[split:]
		summary = a.summarize(early, llmClient)
		for _, h := range recent {
			messages = append(messages, renderHistoryMessage(h))
		}
		return summary, messages
	}

	// 3) 未达阈值：全部保留原文 + 结构化结果回放
	for _, h := range history {
		messages = append(messages, renderHistoryMessage(h))
	}
	return "", messages
}

// renderHistoryMessage 把一条历史消息渲染为 llm.Message，assistant 附带结构化结果回放。
func renderHistoryMessage(h HistoryItem) llm.Message {
	role := h.Role
	if role != "user" && role != "assistant" {
		role = "user"
	}
	content := strings.TrimSpace(h.Content)
	if role == "assistant" && h.Extra != "" {
		if extra := renderExtraForMemory(h.Extra); extra != "" {
			content += "\n\n[上一轮分析附带的资料，供参考]\n" + extra
		}
	}
	return llm.Message{Role: role, Content: content}
}

// renderExtraForMemory 把 assistant 的 extra JSON 转成模型可读的精简文本（图表/表格/SQL）。
func renderExtraForMemory(extra string) string {
	var m struct {
		Chart *ChartSpec                     `json:"chart"`
		Rows  []map[string]interface{}       `json:"rows"`
		SQL   string                         `json:"sql"`
		Steps []map[string]interface{}       `json:"steps"`
	}
	if err := json.Unmarshal([]byte(extra), &m); err != nil {
		return ""
	}
	var sb strings.Builder
	if m.SQL != "" {
		sb.WriteString("执行的 SQL: " + m.SQL + "\n")
	}
	if m.Chart != nil && m.Chart.Title != "" {
		sb.WriteString(fmt.Sprintf("生成的图表(%s): %s, 分类=%v, 系列=%v\n",
			m.Chart.Type, m.Chart.Title, m.Chart.Categories, m.Chart.Series))
	}
	if len(m.Rows) > 0 {
		limit := len(m.Rows)
		if limit > 8 {
			limit = 8
		}
		sb.WriteString("查询返回的数据(前" + fmt.Sprintf("%d", limit) + "行):\n")
		for _, row := range m.Rows[:limit] {
			b, _ := json.Marshal(row)
			sb.WriteString("  " + string(b) + "\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

// summarize 用 LLM 把早期对话压缩成一段中文摘要（记忆摘要）。失败则返回空串（降级为不摘要）。
func (a *Agent) summarize(early []HistoryItem, llmClient *llm.Client) string {
	if len(early) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("以下是较早的对话记录，请压缩为一段简洁的中文摘要，保留关键事实、数据结论、用户意图与已生成的图表/SQL 要点：\n")
	for i, h := range early {
		role := h.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}
		content := strings.TrimSpace(h.Content)
		if role == "assistant" && h.Extra != "" {
			if extra := renderExtraForMemory(h.Extra); extra != "" {
				content += " [" + extra + "]"
			}
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, role, content))
	}
	sb.WriteString("\n仅输出摘要正文，不要加前缀。")

	resp, err := llmClient.Chat([]llm.Message{
		{Role: "system", Content: "你是一个对话摘要器，把多轮对话压缩为简洁、事实导向的中文摘要。"},
		{Role: "user", Content: sb.String()},
	}, nil)
	if err != nil || resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.Content)
}
