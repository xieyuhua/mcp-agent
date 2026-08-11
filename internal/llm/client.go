package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"company.com/mcp-data-server/config"
)

// Client 轻量 OpenAI 兼容 chat 客户端（仅依赖标准库），供后台「AI 一键完善」调用。
type Client struct {
	cfg    config.LLMConfig
	hc     *http.Client
	apiURL string
}

// NewClient 根据配置构造客户端；base_url 为空视为未配置。
func NewClient(cfg config.LLMConfig) *Client {
	if cfg.Provider == "" {
		cfg.Provider = "ollama"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434"
	}
	if cfg.Model == "" {
		cfg.Model = "qwen2.5:14b"
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.3
	}
	return &Client{
		cfg:    cfg,
		hc:     &http.Client{Timeout: 60 * time.Second},
		apiURL: strings.TrimRight(cfg.BaseURL, "/") + "/v1/chat/completions",
	}
}

// Configured 返回是否配置了可用的 LLM（base_url 非空）。
func (c *Client) Configured() bool {
	return c != nil && c.cfg.BaseURL != ""
}

// ChatCompletion 请求单轮对话，返回模型文本。
func (c *Client) ChatCompletion(ctx context.Context, system, user string, maxTokens int) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("未配置大模型（config.json 的 llm.base_url 为空）")
	}
	if maxTokens <= 0 {
		maxTokens = c.cfg.MaxTokens
		if maxTokens <= 0 {
			maxTokens = 512
		}
	}

	reqBody := chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: c.cfg.Temperature,
		MaxTokens:   maxTokens,
		Stream:      false,
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("调用大模型失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("大模型返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out chatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("解析大模型响应失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("大模型未返回内容")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

type chatRequest struct {
	Model       string         `json:"model"`
	Messages    []chatMessage  `json:"messages"`
	Temperature float64        `json:"temperature"`
	MaxTokens   int            `json:"max_tokens"`
	Stream      bool           `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}
