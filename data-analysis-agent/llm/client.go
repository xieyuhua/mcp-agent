package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message 一次对话消息。
type Message struct {
	Role      string     // system | user | assistant | tool
	Content   string
	ToolCalls []ToolCall // assistant 发起的工具调用
	ToolCallID string    // tool 结果消息对应的调用 ID
	Name      string     // tool 结果消息对应的工具名
}

// ToolCall 模型发起的一次工具调用。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // 原始 JSON 字符串
}

// Tool 暴露给模型的工具定义（OpenAI 函数风格）。
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// Response 模型返回。
type Response struct {
	Content   string
	ToolCalls []ToolCall
}

// Client 本地大模型客户端，兼容 Ollama 与 OpenAI 风格 chat 接口。
type Client struct {
	provider    string
	baseURL     string
	model       string
	apiKey      string
	temperature float64
	maxTokens   int
	http        *http.Client
}

// NewClient 构造客户端。
func NewClient(provider, baseURL, model, apiKey string, temperature float64, maxTokens int) *Client {
	return &Client{
		provider:    strings.ToLower(provider),
		baseURL:     strings.TrimRight(baseURL, "/"),
		model:       model,
		apiKey:      apiKey,
		temperature: temperature,
		maxTokens:   maxTokens,
		http:        &http.Client{Timeout: 5 * time.Minute},
	}
}

// Chat 发起一轮对话，tools 为空表示纯对话。
func (c *Client) Chat(messages []Message, tools []Tool) (*Response, error) {
	if len(tools) == 0 {
		return c.chatNoTools(messages)
	}
	if c.provider == "openai" {
		return c.chatOpenAI(messages, tools)
	}
	return c.chatOllama(messages, tools)
}

// ---- Ollama ----

func (c *Client) chatOllama(messages []Message, tools []Tool) (*Response, error) {
	options := map[string]interface{}{"temperature": c.temperature}
	// maxTokens<=0 表示不限制生成长度，避免回答被截断。
	if c.maxTokens > 0 {
		options["num_predict"] = c.maxTokens
	}
	reqBody := map[string]interface{}{
		"model":    c.model,
		"messages": toOllamaMessages(messages),
		"stream":   false,
		"tools":    toOllamaTools(tools),
		"options":  options,
	}
	var out struct {
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		Done bool `json:"done"`
	}
	if err := c.post("/api/chat", reqBody, &out); err != nil {
		return nil, err
	}
	resp := &Response{Content: out.Message.Content}
	for _, tc := range out.Message.ToolCalls {
		args := "{}"
		if len(tc.Function.Arguments) > 0 {
			args = string(tc.Function.Arguments)
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return resp, nil
}

func toOllamaMessages(messages []Message) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		msg := map[string]interface{}{"role": m.Role, "content": m.Content}
		if m.Role == "tool" {
			if m.Name != "" {
				msg["name"] = m.Name
			}
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]interface{}, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				args := json.RawMessage("{}")
				if tc.Arguments != "" {
					args = json.RawMessage(tc.Arguments)
				}
				calls = append(calls, map[string]interface{}{
					"function": map[string]interface{}{
						"name":      tc.Name,
						"arguments": args,
					},
				})
			}
			msg["tool_calls"] = calls
		}
		out = append(out, msg)
	}
	return out
}

func toOllamaTools(tools []Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}
	return out
}

// ---- OpenAI 兼容 ----

func (c *Client) chatOpenAI(messages []Message, tools []Tool) (*Response, error) {
	msgs := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		msg := map[string]interface{}{"role": m.Role, "content": m.Content}
		if m.Role == "tool" {
			if m.ToolCallID != "" {
				msg["tool_call_id"] = m.ToolCallID
			}
			if m.Name != "" {
				msg["name"] = m.Name
			}
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]interface{}, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if args == "" {
					args = "{}"
				}
				calls = append(calls, map[string]interface{}{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      tc.Name,
						"arguments": args,
					},
				})
			}
			msg["tool_calls"] = calls
		}
		msgs = append(msgs, msg)
	}

	toolDefs := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		toolDefs = append(toolDefs, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}

	reqBody := map[string]interface{}{
		"model":       c.model,
		"messages":    msgs,
		"temperature": c.temperature,
		"tools":       toolDefs,
	}
	// maxTokens<=0 表示不限制生成长度，避免回答被截断。
	if c.maxTokens > 0 {
		reqBody["max_tokens"] = c.maxTokens
	}
	var out struct {
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := c.post("/v1/chat/completions", reqBody, &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty choices")
	}
	m := out.Choices[0].Message
	resp := &Response{Content: m.Content}
	for _, tc := range m.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return resp, nil
}

// chatNoTools 不带工具的纯对话（两种 provider 共用 messages 结构）。
func (c *Client) chatNoTools(messages []Message) (*Response, error) {
	if c.provider == "openai" {
		msgs := make([]map[string]interface{}, 0, len(messages))
		for _, m := range messages {
			msgs = append(msgs, map[string]interface{}{"role": m.Role, "content": m.Content})
		}
		reqBody := map[string]interface{}{
			"model":       c.model,
			"messages":    msgs,
			"temperature": c.temperature,
		}
		// maxTokens<=0 表示不限制生成长度，避免回答被截断。
		if c.maxTokens > 0 {
			reqBody["max_tokens"] = c.maxTokens
		}
		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := c.post("/v1/chat/completions", reqBody, &out); err != nil {
			return nil, err
		}
		if len(out.Choices) == 0 {
			return nil, fmt.Errorf("openai: empty choices")
		}
		return &Response{Content: out.Choices[0].Message.Content}, nil
	}
	// Ollama 无工具
	options := map[string]interface{}{"temperature": c.temperature}
	// maxTokens<=0 表示不限制生成长度，避免回答被截断。
	if c.maxTokens > 0 {
		options["num_predict"] = c.maxTokens
	}
	reqBody := map[string]interface{}{
		"model":    c.model,
		"messages": toOllamaMessages(messages),
		"stream":   false,
		"options":  options,
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := c.post("/api/chat", reqBody, &out); err != nil {
		return nil, err
	}
	return &Response{Content: out.Message.Content}, nil
}

// post 发送 HTTP 请求并解析 JSON。
func (c *Client) post(path string, body interface{}, out interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("llm request (%s%s): %w", c.baseURL, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("llm http %d: %s", resp.StatusCode, string(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("llm decode: %w (body: %s)", err, string(data))
	}
	return nil
}
