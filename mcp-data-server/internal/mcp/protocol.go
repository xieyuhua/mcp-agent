package mcp

import "encoding/json"

// Request JSON-RPC 请求。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response JSON-RPC 响应。
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError JSON-RPC 错误。
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Tool MCP 工具定义。
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// CallParams tools/call 参数。
type CallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// CallResult tools/call 返回。
type CallResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}

// Content 返回内容块。
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ServerInfo / Capabilities / InitializeResult 初始化握手返回。
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Capabilities struct {
	Tools map[string]interface{} `json:"tools"`
}

type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
}
