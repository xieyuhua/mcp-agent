package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"company.com/data-analysis-agent/internal/logger"
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
	Duration  time.Duration // 本次请求从发送到接收完成的耗时
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

	// OnLog 调用日志回调，参数为 (logCtx, requestInfo, responseText, duration, errorMsg)。
	OnLog func(logCtx *LogContext, reqInfo map[string]interface{}, respText string, duration time.Duration, errMsg string)
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

// LogContext 调用日志上下文。
type LogContext struct {
	UserID         string
	ConversationID string
}

// Chat 发起一轮对话，tools 为空表示纯对话（不记录调用日志）。
func (c *Client) Chat(messages []Message, tools []Tool) (resp *Response, err error) {
	return c.ChatWithLog(messages, tools, nil)
}

// ChatWithLog 发起一轮对话并记录调用日志（logCtx 非 nil 时）。
func (c *Client) ChatWithLog(messages []Message, tools []Tool, logCtx *LogContext) (resp *Response, err error) {
	logger.Debugf("[llm] 发起对话请求: messages=%d tools=%d(%s) 当前问题=%q", len(messages), len(tools), toolNames(tools), lastUserQuestion(messages))
	t0 := time.Now()
	defer func() {
		if resp != nil {
			resp.Duration = time.Since(t0)
		}
		c.logCall(logCtx, messages, tools, resp, time.Since(t0), err)
	}()
	if len(tools) == 0 {
		resp, err = c.chatNoTools(messages)
		return
	}
	if c.provider == "openai" {
		resp, err = c.chatOpenAI(messages, tools)
		return
	}
	resp, err = c.chatOllama(messages, tools)
	return
}

// lastUserQuestion 返回 messages 中最后一条 user 消息的内容，便于日志追踪自然语言输入。
func lastUserQuestion(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return logger.Sanitize(messages[i].Content)
		}
	}
	return ""
}

// toolNames 返回工具名称列表串，用于日志。
func toolNames(tools []Tool) string {
	names := make([]string, 0, len(tools))
	for _, t := range tools { names = append(names, t.Name) }
	return strings.Join(names, ", ")
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

// ChatStream 同 Chat，但开启流式输出（不记录调用日志）。
func (c *Client) ChatStream(messages []Message, tools []Tool, onToken func(string)) (resp *Response, err error) {
	return c.ChatStreamWithLog(messages, tools, onToken, nil)
}

// ChatStreamWithLog 流式对话并记录调用日志。
func (c *Client) ChatStreamWithLog(messages []Message, tools []Tool, onToken func(string), logCtx *LogContext) (resp *Response, err error) {
	logger.Debugf("[llm] 发起流式对话请求: messages=%d tools=%d(%s) 当前问题=%q", len(messages), len(tools), toolNames(tools), lastUserQuestion(messages))
	t0 := time.Now()
	defer func() {
		if resp != nil {
			resp.Duration = time.Since(t0)
		}
		c.logCall(logCtx, messages, tools, resp, time.Since(t0), err)
	}()
	if onToken == nil {
		onToken = func(string) {}
	}
	if len(tools) == 0 {
		resp, err = c.chatStreamNoTools(messages, onToken)
		return
	}
	if c.provider == "openai" {
		resp, err = c.chatStreamOpenAI(messages, tools, onToken)
		return
	}
	resp, err = c.chatStreamOllama(messages, tools, onToken)
	return
}

// logCall 触发调用日志回调。
func (c *Client) logCall(logCtx *LogContext, messages []Message, tools []Tool, resp *Response, duration time.Duration, err error) {
	if c.OnLog == nil {
		return
	}
	reqInfo := map[string]interface{}{
		"provider": c.provider,
		"model":    c.model,
		"messages": messages,
		"tools":    tools,
	}
	respText := ""
	if resp != nil {
		respText = resp.Content
	}
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	c.OnLog(logCtx, reqInfo, respText, duration, errMsg)
}


// ---- 流式底层：发起请求并返回响应体（由调用方逐行读取）----

func (c *Client) streamPost(path string, body interface{}) (io.ReadCloser, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	logger.Infof("[llm] 流式请求: %s%s 模型=%s", c.baseURL, path, c.model)
	t0 := time.Now()
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		logger.Errorf("[llm] 流式请求失败: %s%s err=%v", c.baseURL, path, err)
		return nil, fmt.Errorf("llm stream request (%s%s): %w", c.baseURL, path, err)
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		logger.Errorf("[llm] 流式请求 HTTP %d: %s%s", resp.StatusCode, c.baseURL, path)
		return nil, fmt.Errorf("llm http %d: %s", resp.StatusCode, string(data))
	}
	logger.Infof("[llm] 流式请求已建立连接: %s%s 耗时=%s", c.baseURL, path, time.Since(t0))
	return resp.Body, nil
}

// forEachSSELine 按行读取 SSE 流，对每条 data: 负载调用 fn；遇到 [DONE] 正常结束。
func forEachSSELine(body io.Reader, fn func(data string) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return nil
		}
		if err := fn(data); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// forEachNDJSONLine 按行读取 NDJSON 流（Ollama 流式格式），对每行调用 fn。
func forEachNDJSONLine(body io.Reader, fn func(line string) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := fn(line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// ---- Ollama 流式 ----

func (c *Client) chatStreamOllama(messages []Message, tools []Tool, onToken func(string)) (*Response, error) {
	options := map[string]interface{}{"temperature": c.temperature}
	if c.maxTokens > 0 {
		options["num_predict"] = c.maxTokens
	}
	reqBody := map[string]interface{}{
		"model":    c.model,
		"messages": toOllamaMessages(messages),
		"stream":   true,
		"tools":    toOllamaTools(tools),
		"options":  options,
	}
	body, err := c.streamPost("/api/chat", reqBody)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var content strings.Builder
	var toolCalls []ToolCall
	e := forEachNDJSONLine(body, func(line string) error {
		var chunk struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return nil // 跳过无法解析的脏行
		}
		if chunk.Message.Content != "" {
			content.WriteString(chunk.Message.Content)
			onToken(chunk.Message.Content)
		}
		for _, tc := range chunk.Message.ToolCalls {
			args := "{}"
			if len(tc.Function.Arguments) > 0 {
				args = string(tc.Function.Arguments)
			}
			toolCalls = append(toolCalls, ToolCall{Name: tc.Function.Name, Arguments: args})
		}
		return nil
	})
	if e != nil {
		return nil, fmt.Errorf("llm stream read: %w", e)
	}
	return &Response{Content: content.String(), ToolCalls: toolCalls}, nil
}

// ---- OpenAI 兼容流式 ----

type openAIToolAcc struct {
	ID   string
	Name string
	Args strings.Builder
}

// mergeOpenAIToolCall 按 index 合并分片到达的 tool_calls（arguments 可能被拆成多块）。
func mergeOpenAIToolCall(acc *[]openAIToolAcc, index int, id, name, args string) {
	for i := len(*acc); i <= index; i++ {
		*acc = append(*acc, openAIToolAcc{})
	}
	a := &(*acc)[index]
	if id != "" {
		a.ID = id
	}
	if name != "" {
		a.Name = name
	}
	if args != "" {
		a.Args.WriteString(args)
	}
}

func (c *Client) chatStreamOpenAI(messages []Message, tools []Tool, onToken func(string)) (*Response, error) {
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
		"stream":      true,
	}
	if c.maxTokens > 0 {
		reqBody["max_tokens"] = c.maxTokens
	}
	body, err := c.streamPost("/v1/chat/completions", reqBody)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var content strings.Builder
	var tcAcc []openAIToolAcc
	e := forEachSSELine(body, func(data string) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			content.WriteString(d.Content)
			onToken(d.Content)
		}
		for _, tc := range d.ToolCalls {
			mergeOpenAIToolCall(&tcAcc, tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments)
		}
		return nil
	})
	if e != nil {
		return nil, fmt.Errorf("llm stream read: %w", e)
	}
	resp := &Response{Content: content.String()}
	for _, acc := range tcAcc {
		args := acc.Args.String()
		if args == "" {
			args = "{}"
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: acc.ID, Name: acc.Name, Arguments: args})
	}
	return resp, nil
}

// ---- 无工具流式（纯对话）----

func (c *Client) chatStreamNoTools(messages []Message, onToken func(string)) (*Response, error) {
	if c.provider == "openai" {
		return c.chatStreamNoToolsOpenAI(messages, onToken)
	}
	return c.chatStreamNoToolsOllama(messages, onToken)
}

func (c *Client) chatStreamNoToolsOllama(messages []Message, onToken func(string)) (*Response, error) {
	options := map[string]interface{}{"temperature": c.temperature}
	if c.maxTokens > 0 {
		options["num_predict"] = c.maxTokens
	}
	reqBody := map[string]interface{}{
		"model":    c.model,
		"messages": toOllamaMessages(messages),
		"stream":   true,
		"options":  options,
	}
	body, err := c.streamPost("/api/chat", reqBody)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var content strings.Builder
	e := forEachNDJSONLine(body, func(line string) error {
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return nil
		}
		if chunk.Message.Content != "" {
			content.WriteString(chunk.Message.Content)
			onToken(chunk.Message.Content)
		}
		return nil
	})
	if e != nil {
		return nil, fmt.Errorf("llm stream read: %w", e)
	}
	return &Response{Content: content.String()}, nil
}

func (c *Client) chatStreamNoToolsOpenAI(messages []Message, onToken func(string)) (*Response, error) {
	msgs := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		msgs = append(msgs, map[string]interface{}{"role": m.Role, "content": m.Content})
	}
	reqBody := map[string]interface{}{
		"model":       c.model,
		"messages":    msgs,
		"temperature": c.temperature,
		"stream":      true,
	}
	if c.maxTokens > 0 {
		reqBody["max_tokens"] = c.maxTokens
	}
	body, err := c.streamPost("/v1/chat/completions", reqBody)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var content strings.Builder
	e := forEachSSELine(body, func(data string) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		if chunk.Choices[0].Delta.Content != "" {
			content.WriteString(chunk.Choices[0].Delta.Content)
			onToken(chunk.Choices[0].Delta.Content)
		}
		return nil
	})
	if e != nil {
		return nil, fmt.Errorf("llm stream read: %w", e)
	}
	return &Response{Content: content.String()}, nil
}

// post 发送 HTTP 请求并解析 JSON。
func (c *Client) post(path string, body interface{}, out interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	logger.Infof("[llm] 请求: %s%s 模型=%s", c.baseURL, path, c.model)
	t0 := time.Now()
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
		logger.Errorf("[llm] 请求失败: %s%s err=%v", c.baseURL, path, err)
		return fmt.Errorf("llm request (%s%s): %w", c.baseURL, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		logger.Errorf("[llm] 请求 HTTP %d: %s%s", resp.StatusCode, c.baseURL, path)
		return fmt.Errorf("llm http %d: %s", resp.StatusCode, string(data))
	}
	logger.Infof("[llm] 请求完成: %s%s 耗时=%s", c.baseURL, path, time.Since(t0))
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("llm decode: %w (body: %s)", err, string(data))
	}
	return nil
}
