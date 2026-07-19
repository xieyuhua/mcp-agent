// Package skill 提供「技能」的加载与解析能力。
//
// 技能（skill）是一段预定义的工作流指引，以 .md 文件存放在 skills 目录下，
// 文件头部用 --- 包裹的 YAML frontmatter 声明 name / description，其余正文即工作流内容。
// Agent 启动时扫描该目录加载全部技能，并向大模型暴露 use_skill 工具；
// 模型在判断用户需求匹配某技能场景时调用 use_skill，Agent 把对应技能正文回灌给模型，
// 模型随后严格按该工作流执行（与既有 ReAct 循环、MCP 工具完全兼容）。
//
// 设计目标：零外部依赖（仅用 Go 标准库），frontmatter 解析足够覆盖 name/description，
// 不引入 yaml 第三方库，保持与项目「零外部依赖」原则一致。
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill 一个技能定义。
type Skill struct {
	// Name 唯一名称，同时作为 use_skill 工具的 name 参数。
	Name string `json:"name"`
	// Description 何时使用该技能的描述，供 LLM 判断是否调用（也展示在 use_skill 工具说明里）。
	Description string `json:"description"`
	// Body 正文（frontmatter 之后的 Markdown 工作流指引），加载后回灌给模型。
	Body string `json:"-"`
}

// LoadDir 扫描目录加载所有技能文件（.md，忽略 readme.md）。
// 目录不存在 / 为空 / 单个文件解析失败均不致命：返回已成功加载的技能 map，并尽量返回首个错误。
func LoadDir(dir string) (map[string]*Skill, error) {
	skills := make(map[string]*Skill)
	if strings.TrimSpace(dir) == "" {
		return skills, nil
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		// 目录不存在不致命，视为「未配置技能」。
		return skills, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return skills, err
	}
	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		if !strings.HasSuffix(lower, ".md") || lower == "readme.md" {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			if firstErr == nil {
				firstErr = rerr
			}
			continue
		}
		s, perr := Parse(string(data))
		if perr != nil || s.Name == "" {
			// 解析失败仅跳过该文件（告警由调用方日志处理）。
			continue
		}
		skills[s.Name] = s
	}
	return skills, firstErr
}

// Parse 解析单个技能文件内容（--- frontmatter --- + 正文）。
func Parse(content string) (*Skill, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return nil, fmt.Errorf("技能文件缺少 frontmatter（应以 --- 开头）")
	}
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, fmt.Errorf("技能文件 frontmatter 未闭合（缺少第二行 ---）")
	}
	front := rest[:idx]
	body := strings.TrimSpace(rest[idx+4:])
	meta, err := parseFrontmatter(front)
	if err != nil {
		return nil, err
	}
	return &Skill{
		Name:        strings.TrimSpace(meta["name"]),
		Description: strings.TrimSpace(meta["description"]),
		Body:        body,
	}, nil
}

// parseFrontmatter 极简 YAML 解析：支持 `key: value` 与块标量 `key: |`（多行）。
// 仅覆盖技能所需的 name / description，避免引入第三方 yaml 库。
func parseFrontmatter(s string) (map[string]string, error) {
	out := make(map[string]string)
	lines := strings.Split(s, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			i++
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		// 块标量（| 或 >）：收集后续缩进行作为多行值。
		if val == "" || val == "|" || val == ">" {
			var sb strings.Builder
			i++
			for i < len(lines) {
				nl := lines[i]
				if strings.TrimSpace(nl) == "" {
					sb.WriteString("\n")
					i++
					continue
				}
				// 缩进行（以空格/Tab 开头）属于块内容；前导缩进去掉（frontmatter 为散文，无需保留）。
				if len(nl) > 0 && (nl[0] == ' ' || nl[0] == '\t') {
					sb.WriteString(strings.TrimSpace(nl) + "\n")
					i++
				} else {
					break
				}
			}
			out[key] = strings.TrimSpace(sb.String())
			continue
		}
		out[key] = val
		i++
	}
	return out, nil
}

// Names 返回技能名称列表（按字典序，输出稳定）。
func Names(skills map[string]*Skill) []string {
	names := make([]string, 0, len(skills))
	for n := range skills {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ToolDescription 生成 use_skill 工具的描述文本：列出所有可用技能（名称 -> 适用场景），
// 让 LLM 能据此判断何时调用。无技能时返回空串。
func ToolDescription(skills map[string]*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("调用一个预定义技能（工作流指引）来指导本轮分析与操作。可用技能如下（name -> 适用场景）：\n")
	for _, n := range Names(skills) {
		sb.WriteString("- " + n + ": " + skills[n].Description + "\n")
	}
	sb.WriteString("当用户需求匹配某技能场景时，调用本工具加载该技能，并严格按其正文指引执行后续步骤。")
	return sb.String()
}
