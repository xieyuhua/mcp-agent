package mcpclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// errMatched 用于在 SSE 解析中标记已找到匹配 id 的响应。
var errMatched = errors.New("matched")

// RemoteConfig 远程 MCP 服务连接配置。
type RemoteConfig struct {
	// BaseURL 远程地址，如 http://host:9000/mcp 或 http://host:9000/sse
	BaseURL string `json:"base_url"`
	// Transport 传输方式："streamable-http"（默认，推荐）| "sse"（旧版）
	Transport string `json:"transport"`
	// APIKey 可选鉴权，放入 Authorization: Bearer
	APIKey string `json:"api_key"`
	// Headers 额外请求头（如自定义鉴权）
	Headers map[string]string `json:"headers"`
	// Timeout 单次请求超时（SSE 模式不用于长连接本身）
	Timeout time.Duration `json:"timeout"`
}

// StartRemote 连接远程 MCP 服务并完成握手。
func StartRemote(cfg RemoteConfig) (*Client, error) {
	var t Transport
	var err error
	switch strings.ToLower(cfg.Transport) {
	case "sse":
		t, err = newSSETransport(cfg)
	default:
		t, err = newHTTPTransport(cfg)
	}
	if err != nil {
		return nil, err
	}
	if err := t.Initialize(); err != nil {
		t.Close()
		return nil, err
	}
	return &Client{t: t}, nil
}

// rpcRequest JSON-RPC 请求。
type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

func buildHeaders(cfg RemoteConfig) map[string]string {
	h := map[string]string{}
	for k, v := range cfg.Headers {
		h[k] = v
	}
	if cfg.APIKey != "" {
		h["Authorization"] = "Bearer " + cfg.APIKey
	}
	return h
}

// ---- Streamable HTTP 传输（默认，推荐）----

type httpTransport struct {
	baseURL   string
	client    *http.Client
	headers   map[string]string
	mu        sync.Mutex
	nextID    int
	tools     []ToolMeta
	sessionID string
}

func newHTTPTransport(cfg RemoteConfig) (*httpTransport, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("remote mcp base_url is empty")
	}
	to := cfg.Timeout
	if to == 0 {
		to = 30 * time.Second
	}
	return &httpTransport{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		client:  &http.Client{Timeout: to},
		headers: buildHeaders(cfg),
	}, nil
}

func (t *httpTransport) Initialize() error {
	if err := t.Call("initialize", map[string]interface{}{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "data-analysis-agent", "version": "1.0.0"},
	}, nil); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := t.Notify("notifications/initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("initialized notify: %w", err)
	}
	var list struct {
		Tools []ToolMeta `json:"tools"`
	}
	if err := t.Call("tools/list", map[string]interface{}{}, &list); err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	t.tools = list.Tools
	return nil
}

func (t *httpTransport) Tools() []ToolMeta { return t.tools }

func (t *httpTransport) Notify(method string, params interface{}) error {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	return t.post(req, nil)
}

func (t *httpTransport) Call(method string, params interface{}, out interface{}) error {
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	t.mu.Unlock()
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	return t.post(req, out)
}

func (t *httpTransport) post(req rpcRequest, out interface{}) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequest(http.MethodPost, t.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}
	if t.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.sessionID)
	}

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post %s: %w", t.baseURL, err)
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionID = sid
	}
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return readSSEResponse(bufio.NewReader(resp.Body), req.ID, out)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		// 通知（notification）通常返回 202 空响应体，无需解析
		return nil
	}
	return decodeRPC(data, req.ID, out)
}

func (t *httpTransport) Close() error { return nil }

// decodeRPC 解析普通 JSON-RPC 响应，校验 id 匹配。
func decodeRPC(data []byte, wantID int, out interface{}) error {
	var resp struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("decode rpc: %w (raw: %s)", err, string(data))
	}
	if resp.Error != nil {
		return fmt.Errorf("rpc error: %s", resp.Error.Message)
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}

// readSSEResponse 从 SSE 流中读取，直到匹配 wantID 的响应。
func readSSEResponse(r *bufio.Reader, wantID int, out interface{}) error {
	var dataLines []string
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		raw := strings.Join(dataLines, "\n")
		dataLines = nil
		var ev struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return nil // 忽略非 JSON 事件
		}
		if ev.Error != nil {
			return fmt.Errorf("rpc error: %s", ev.Error.Message)
		}
		if ev.ID != nil {
			var got int
			if json.Unmarshal(ev.ID, &got) == nil && got == wantID {
				if out != nil && len(ev.Result) > 0 {
					if err := json.Unmarshal(ev.Result, out); err != nil {
						return fmt.Errorf("decode result: %w", err)
					}
				}
				return errMatched
			}
		}
		return nil
	}
	for {
		line, err := r.ReadString('\n')
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		} else if strings.TrimSpace(line) == "" {
			if ferr := flush(); ferr != nil {
				if errors.Is(ferr, errMatched) {
					return nil
				}
				return ferr
			}
		}
		if err != nil {
			ferr := flush()
			if errors.Is(ferr, errMatched) {
				return nil
			}
			if ferr != nil {
				return ferr
			}
			return fmt.Errorf("sse 流结束，未收到匹配 id=%d 的响应", wantID)
		}
	}
}

// ---- SSE 传输（旧版 MCP 远程模式：GET /sse 收 + POST /messages 发）----

type sseTransport struct {
	baseURL  string
	endpoint string
	client   *http.Client
	headers  map[string]string
	mu       sync.Mutex
	nextID   int
	tools    []ToolMeta
	pending  map[int]chan sseResult
	closed   chan struct{}
}

type sseResult struct {
	result json.RawMessage
	err    error
}

func newSSETransport(cfg RemoteConfig) (*sseTransport, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("remote mcp base_url is empty")
	}
	t := &sseTransport{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		client:  &http.Client{},
		headers: buildHeaders(cfg),
		pending: map[int]chan sseResult{},
		closed:  make(chan struct{}),
	}
	if err := t.openStream(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *sseTransport) openStream() error {
	req, _ := http.NewRequest(http.MethodGet, t.baseURL, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("open sse stream: %w", err)
	}
	go t.readLoop(resp.Body)
	return nil
}

func (t *sseTransport) readLoop(rc io.ReadCloser) {
	defer rc.Close()
	r := bufio.NewReader(rc)
	var dataLines []string
	eventName := ""
	flush := func() {
		if len(dataLines) == 0 {
			eventName = ""
			return
		}
		raw := strings.Join(dataLines, "\n")
		dataLines = nil
		switch eventName {
		case "endpoint":
			t.endpoint = raw
		default:
			var ev struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(raw), &ev) == nil {
				if ev.Error != nil {
					t.deliver(ev.ID, nil, fmt.Errorf("rpc error: %s", ev.Error.Message))
					return
				}
				t.deliver(ev.ID, ev.Result, nil)
			}
		}
		eventName = ""
	}
	for {
		select {
		case <-t.closed:
			return
		default:
		}
		line, err := r.ReadString('\n')
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		} else if strings.TrimSpace(line) == "" {
			flush()
		}
		if err != nil {
			return
		}
	}
}

func (t *sseTransport) deliver(idRaw json.RawMessage, result json.RawMessage, rerr error) {
	var id int
	if json.Unmarshal(idRaw, &id) != nil {
		return
	}
	t.mu.Lock()
	ch, ok := t.pending[id]
	t.mu.Unlock()
	if ok {
		ch <- sseResult{result: result, err: rerr}
	}
}

func (t *sseTransport) Initialize() error {
	deadline := time.Now().Add(10 * time.Second)
	for t.endpoint == "" {
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 sse endpoint 超时")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := t.Call("initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "data-analysis-agent", "version": "1.0.0"},
	}, nil); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := t.Notify("notifications/initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("initialized notify: %w", err)
	}
	var list struct {
		Tools []ToolMeta `json:"tools"`
	}
	if err := t.Call("tools/list", map[string]interface{}{}, &list); err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	t.tools = list.Tools
	return nil
}

func (t *sseTransport) Tools() []ToolMeta { return t.tools }

func (t *sseTransport) postURL() string {
	if strings.HasPrefix(t.endpoint, "http") {
		return t.endpoint
	}
	return joinURL(t.baseURL, t.endpoint)
}

func (t *sseTransport) Notify(method string, params interface{}) error {
	if t.endpoint == "" {
		return fmt.Errorf("sse endpoint 尚未就绪")
	}
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest(http.MethodPost, t.postURL(), bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (t *sseTransport) Call(method string, params interface{}, out interface{}) error {
	if t.endpoint == "" {
		return fmt.Errorf("sse endpoint 尚未就绪")
	}
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	ch := make(chan sseResult, 1)
	t.pending[id] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
	}()

	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest(http.MethodPost, t.postURL(), bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return err
	}
	resp.Body.Close()

	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out != nil && len(r.result) > 0 {
			if err := json.Unmarshal(r.result, out); err != nil {
				return fmt.Errorf("decode result: %w", err)
			}
		}
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("等待 sse 响应超时 id=%d", id)
	}
}

func (t *sseTransport) Close() error {
	select {
	case <-t.closed:
	default:
		close(t.closed)
	}
	return nil
}

// joinURL 把相对路径拼到 base 的 host 上。
func joinURL(base, rel string) string {
	if strings.HasPrefix(rel, "http") {
		return rel
	}
	if strings.HasSuffix(base, "/") {
		return base + strings.TrimPrefix(rel, "/")
	}
	return base + "/" + strings.TrimPrefix(rel, "/")
}
