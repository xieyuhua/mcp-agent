package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// sanitizeSkillName 将任意字符串转为合法的 skill 文件名（小写字母数字+下划线）。
func sanitizeSkillName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "conversation"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '：' || r == ':' {
			b.WriteRune('_')
		}
	}
	name := b.String()
	// 去除连续下划线
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	name = strings.Trim(name, "_")
	if len(name) > 48 {
		name = name[:48]
	}
	if name == "" {
		name = "conversation"
	}
	return name
}

const autoSkillPrefix = "auto_"
const defaultAutoSkillMaxKeep = 20

// cleanAutoSkills 清理 skills 目录中 auto_ 前缀的旧技能文件，最多保留 maxKeep 个。
func cleanAutoSkills(dir string, maxKeep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type sf struct {
		name string
		info os.DirEntry
	}
	var autoFiles []sf
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(strings.ToLower(e.Name()), autoSkillPrefix) && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			autoFiles = append(autoFiles, sf{name: e.Name(), info: e})
		}
	}
	if len(autoFiles) <= maxKeep {
		return
	}
	// 按文件名排序（前缀相同 = 按时间/序号排序），保留最新的 maxKeep 个
	// 使用 sort.SliceStable 保持原始顺序
	sort.Slice(autoFiles, func(i, j int) bool {
		return autoFiles[i].name < autoFiles[j].name
	})
	for _, f := range autoFiles[:len(autoFiles)-maxKeep] {
		os.Remove(filepath.Join(dir, f.name))
	}
}

// CompressToSkill 将完整对话历史压缩为 skill 文件，保存到技能目录并热重载。
// 生成的 skill 包含对话目标、分析过程、SQL 要点与结论，供后续 agent 通过 use_skill 自主复用。
// 每个对话仅生成一次（由上层 tryCompressConversation 保证），
// 文件以 auto_ 前缀命名以便自动清理，最多保留 20 个自动生成的技能。
func (a *Agent) CompressToSkill(history []HistoryItem, convTitle, convID string) error {
	if len(history) < 2 {
		return nil
	}

	llmClient := a.llm
	if llmClient == nil {
		return fmt.Errorf("llm client not available")
	}

	dir := a.cfg.SkillsDir
	if dir == "" {
		dir = "skills"
	}

	// 确保目录存在
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}

	// 清理旧 auto 技能（从运行配置读取保留上限）
	maxKeep := a.cfg.Agent.AutoSkillMaxKeep
	if maxKeep <= 0 {
		maxKeep = defaultAutoSkillMaxKeep
	}
	cleanAutoSkills(dir, maxKeep)

	// 构造 LLM 摘要提示
	var sb strings.Builder
	sb.WriteString("以下是一段多轮数据分析对话的全部记录，请将其压缩为一个可复用的技能（skill）工作流。\n\n")
	for i, h := range history {
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
		sb.WriteString(fmt.Sprintf("#%d [%s] %s\n", i+1, role, content))
	}

	prompt := fmt.Sprintf(`基于以上对话，生成一个可复用的技能（skill），输出格式为三段：

name: <英文或拼音的简短技能名，20字符以内>
description: <一句话中文描述，30字以内，说明该技能的用途>

<技能正文：用中文写一个结构化的数据分析工作流，包含：
1. 分析目标（从对话中提炼）
2. 前置条件（需要的表、工具等）
3. 详细步骤（按顺序，可包含参考 SQL 片段）
4. 输出要求
5. 注意事项

注意：技能正文要通用化，去除具体数值，保留方法论。不要提及"用户说"或"本轮对话"。>

对话标题：%s
对话ID：%s

仅输出以上三段内容，不要多余的解释。`, convTitle, convID)

	resp, err := llmClient.Chat([]llm.Message{
		{Role: "system", Content: "你是一个技能提取器，从数据分析对话中提炼可复用的工作流程，输出格式为 name + description + body。"},
		{Role: "user", Content: sb.String() + "\n\n" + prompt},
	}, nil)
	if err != nil || resp == nil || resp.Content == "" {
		return fmt.Errorf("llm summarize failed: %w", err)
	}

	text := strings.TrimSpace(resp.Content)

	// 解析 name、description、body
	skillName := sanitizeSkillName(convTitle)
	if skillName == "" || skillName == "conversation" {
		skillName = "conv_" + convID[:8]
	}
	skillDesc := ""

	lines := strings.Split(text, "\n")
	var nameLine, descLine string
	var bodyLines []string
	inBody := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "name:") {
			nameLine = strings.TrimSpace(trimmed[5:])
		} else if strings.HasPrefix(strings.ToLower(trimmed), "description:") {
			descLine = strings.TrimSpace(trimmed[12:])
		} else if trimmed != "" {
			if nameLine != "" && descLine != "" && !inBody {
				inBody = true
			}
			if inBody {
				bodyLines = append(bodyLines, line)
			}
		}
	}

	if nameLine != "" {
		skillName = sanitizeSkillName(nameLine)
		if skillName == "" {
			skillName = sanitizeSkillName(convTitle)
		}
	}
	if descLine != "" {
		skillDesc = descLine
	}
	body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
	if body == "" {
		body = text
	}

	// 使用 auto_ 前缀 + 时间戳命名，避免与手动创建的 skill 冲突
	ts := time.Now().Format("150405")
	autoName := autoSkillPrefix + skillName + "_" + ts
	skillContent := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", autoName, skillDesc, body)
	skillPath := filepath.Join(dir, autoName+".md")

	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		return fmt.Errorf("write skill file: %w", err)
	}

	// 热重载技能
	if err := a.ReloadSkills(); err != nil {
		return fmt.Errorf("reload skills: %w", err)
	}

	return nil
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
