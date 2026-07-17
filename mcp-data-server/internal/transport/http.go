package transport

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"company.com/mcp-data-server/internal/mcp"
)

// HTTPServer 把 MCP 协议通过 HTTP 暴露，支持两种标准传输：
//   - streamable-http（默认，推荐）：POST 同一端点，响应可为 JSON 或 SSE 流
//   - sse（旧版）：GET /sse 建立接收流 + POST /messages 发送请求
//
// 这样任意标准 MCP 客户端（含其他 Agent 智能体）都能通过 HTTP 对接本服务。
type HTTPServer struct {
	server  *mcp.Server
	httpPath string // streamable-http 端点，默认 /mcp
	ssePath  string // 旧版 sse 端点，默认 /sse
	msgPath  string // 旧版 sse 消息端点，默认 /messages

	mu       sync.Mutex
	sessions map[string]chan []byte // sse 模式：sessionId -> 响应通道
}

// NewHTTPServer 构造 HTTP 传输层。
func NewHTTPServer(server *mcp.Server) *HTTPServer {
	return &HTTPServer{
		server:   server,
		httpPath: "/mcp",
		ssePath:  "/sse",
		msgPath:  "/messages",
		sessions: map[string]chan []byte{},
	}
}

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "sess-fallback"
	}
	return hex.EncodeToString(b)
}

// HandleStreamable 处理 streamable-http 请求（POST /mcp）。
func (h *HTTPServer) HandleStreamable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	// 会话：initialize 时若客户端未带会话则生成新会话
	sid := r.Header.Get("Mcp-Session-Id")
	if msg.Method == "initialize" && sid == "" {
		sid = newSessionID()
	}

	resp, herr := h.server.Handle(r.Context(), body)
	if herr != nil {
		writeJSONErr(w, sid, -32603, herr.Error())
		return
	}
	if msg.ID == nil {
		// 通知：无需响应体
		if sid != "" {
			w.Header().Set("Mcp-Session-Id", sid)
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "text/event-stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		if sid != "" {
			w.Header().Set("Mcp-Session-Id", sid)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(resp))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

// HandleSSE 处理旧版 SSE 传输的接收流（GET /sse）。
func (h *HTTPServer) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sid := newSessionID()
	ch := make(chan []byte, 16)
	h.mu.Lock()
	h.sessions[sid] = ch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.sessions, sid)
		h.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// 告知客户端发送消息的端点（带 sessionId）
	fmt.Fprintf(w, "event: endpoint\ndata: %s?sessionId=%s\n\n", h.msgPath, sid)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(data))
			flusher.Flush()
		}
	}
}

// HandleMessages 处理旧版 SSE 传输的消息发送（POST /messages?sessionId=xxx）。
func (h *HTTPServer) HandleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sid := r.URL.Query().Get("sessionId")
	if sid == "" {
		http.Error(w, "missing sessionId", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	ch, ok := h.sessions[sid]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "unknown or expired session", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var msg struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(body, &msg)
	resp, herr := h.server.Handle(r.Context(), body)
	if herr != nil {
		resp, _ = json.Marshal(map[string]interface{}{
			"jsonrpc": "2.0",
			"error":   map[string]interface{}{"code": -32603, "message": herr.Error()},
		})
	}
	if msg.ID != nil && resp != nil {
		select {
		case ch <- resp:
		default:
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

func writeJSONErr(w http.ResponseWriter, sid string, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	if sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	w.WriteHeader(http.StatusOK)
	b, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"error":   map[string]interface{}{"code": code, "message": msg},
	})
	w.Write(b)
}
