package mcp

import (
	"context"
	"encoding/json"
)

// CallHandler 工具调用处理函数签名。
type CallHandler func(ctx context.Context, name string, args map[string]interface{}) (interface{}, error)

// Server MCP 服务端：负责 JSON-RPC 方法分发。
type Server struct {
	name    string
	version string
	tools   []Tool
	handler CallHandler
}

func NewServer(name, version string, tools []Tool, handler CallHandler) *Server {
	return &Server{name: name, version: version, tools: tools, handler: handler}
}

// Handle 处理一条 JSON-RPC 请求，返回响应字节（通知返回 nil）。
func (s *Server) Handle(ctx context.Context, raw []byte) ([]byte, error) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		// 容错：剥离可能的 BOM / 前导空白等非 JSON 前缀后重试
		if cleaned := extractJSON(raw); cleaned != nil {
			if e2 := json.Unmarshal(cleaned, &req); e2 == nil {
				return s.dispatch(ctx, &req)
			}
		}
		return s.errResp(nil, -32700, "parse error")
	}
	return s.dispatch(ctx, &req)
}

// dispatch 在请求已解析后执行方法分发。
func (s *Server) dispatch(ctx context.Context, req *Request) ([]byte, error) {
	switch req.Method {
	case "initialize":
		return s.okResp(req.ID, InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities:    Capabilities{Tools: map[string]interface{}{}},
			ServerInfo:      ServerInfo{Name: s.name, Version: s.version},
		})
	case "ping":
		return s.okResp(req.ID, map[string]interface{}{})
	case "notifications/initialized":
		return nil, nil // 通知，无需响应
	case "tools/list":
		return s.okResp(req.ID, map[string]interface{}{"tools": s.tools})
	case "tools/call":
		var p CallParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return s.errResp(req.ID, -32602, "invalid params")
		}
		res, err := s.handler(ctx, p.Name, p.Arguments)
		if err != nil {
			return s.okResp(req.ID, CallResult{
				IsError: true,
				Content: []Content{{Type: "text", Text: err.Error()}},
			})
		}
		return s.okResp(req.ID, CallResult{
			Content: []Content{{Type: "text", Text: toJSONString(res)}},
		})
	default:
		return s.errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) okResp(id json.RawMessage, result interface{}) ([]byte, error) {
	resp := Response{JSONRPC: "2.0", ID: rawToInterface(id), Result: result}
	return json.Marshal(resp)
}

func (s *Server) errResp(id json.RawMessage, code int, msg string) ([]byte, error) {
	resp := Response{JSONRPC: "2.0", ID: rawToInterface(id), Error: &RPCError{Code: code, Message: msg}}
	return json.Marshal(resp)
}

func rawToInterface(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var v interface{}
	_ = json.Unmarshal(raw, &v)
	return v
}

func toJSONString(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

// extractJSON 从可能带有 BOM/前导空白的字节中截取第一个 JSON 对象。
func extractJSON(raw []byte) []byte {
	start := -1
	for i, c := range raw {
		if c == '{' {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	depth := 0
	for i := start; i < len(raw); i++ {
		switch raw[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return nil
}
