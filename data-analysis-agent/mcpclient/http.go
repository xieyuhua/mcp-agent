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
	// 地址以 /sse 结尾时，自动使用 SSE 传输（避免用户填了 sse 地址却误选 streamable-http）。
	base := strings.ToLower(strings.TrimSpace(cfg.BaseURL))
	if strings.HasSuffix(strings.TrimRight(base, "/"), "/sse") {
		cfg.Transport = "sse"
	}
	// 地址智能补全：支持简写（如 8081/sse、127.0.0.1:8081/sse）。
	cfg.BaseURL = NormalizeBaseURL(cfg.BaseURL, strings.ToLower(cfg.Transport) == "sse")
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

// normalizeBaseURL 对远程 MCP 地址做智能补全，提升配置友好度（对接 llama.cpp 等本地服务）：
//   - 去除首尾空白；
//   - 缺协议时默认补 http://（本地 MCP 服务几乎都是 http）；
//   - 仅填端口（如 8081 或 8081/sse）时，自动补默认 host 127.0.0.1；
//   - sse=true 时若地址末尾没有 /sse 路径，自动追加（便于直接填 8081 即可对接 llama.cpp）。
//
// 例：
//
//	8081/sse          -> http://127.0.0.1:8081/sse
//	127.0.0.1:8081    -> http://127.0.0.1:8081
//	127.0.0.1:8081/sse-> http://127.0.0.1:8081/sse
//	http://host:9000/mcp -> http://host:9000/mcp（已有协议，原样保留）
func NormalizeBaseURL(raw string, sse bool) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return ""
	}
	// 仅端口或 端口/路径（以数字开头且不包含 host 分隔符）：补默认 host。
	if base[0] >= '0' && base[0] <= '9' && !strings.Contains(base, ":") && !strings.Contains(base, ".") {
		base = "127.0.0.1:" + base
	}
	// 缺协议：补 http://。
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	// SSE 传输：自动补全 /sse 路径（llama.cpp 默认端点）。
	if sse {
		trimmed := strings.TrimRight(base, "/")
		if !strings.HasSuffix(trimmed, "/sse") {
			base = trimmed + "/sse"
		}
	}
	return base
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
	sessionMu sync.RWMutex
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
	}, nil, nil); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := t.Notify("notifications/initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("initialized notify: %w", err)
	}
	var list struct {
		Tools []ToolMeta `json:"tools"`
	}
	if err := t.Call("tools/list", map[string]interface{}{}, &list, nil); err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	t.tools = list.Tools
	return nil
}

func (t *httpTransport) Tools() []ToolMeta { return t.tools }

func (t *httpTransport) Notify(method string, params interface{}) error {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	return t.post(req, nil, nil)
}

func (t *httpTransport) Call(method string, params interface{}, out interface{}, onProgress func(message string)) error {
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	t.mu.Unlock()
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	return t.post(req, out, onProgress)
}

func (t *httpTransport) post(req rpcRequest, out interface{}, onProgress func(message string)) error {
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
	t.sessionMu.RLock()
	sid := t.sessionID
	t.sessionMu.RUnlock()
	if sid != "" {
		httpReq.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post %s: %w", t.baseURL, err)
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionMu.Lock()
		t.sessionID = sid
		t.sessionMu.Unlock()
	}
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return readSSEResponse(bufio.NewReader(resp.Body), req.ID, out, onProgress)
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
// 期间若出现 notifications/progress（无 id），则通过 onProgress 回调（进度文本）实时传出，实现流式。
func readSSEResponse(r *bufio.Reader, wantID int, out interface{}, onProgress func(message string)) error {
	var dataLines []string
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		raw := strings.Join(dataLines, "\n")
		dataLines = nil
		var ev struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Message string `json:"message"`
			} `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return nil // 忽略非 JSON 事件
		}
		// 进度通知：实时回调，不中断读取
		if ev.Method == "notifications/progress" {
			if onProgress != nil && ev.Params.Message != "" {
				onProgress(ev.Params.Message)
			}
			return nil
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
	onProgress func(message string) // 当前 Call 的进度回调（Agent 串行调用工具，单槽足够）
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
			if raw != "" {
				t.endpoint = raw
			}
		default:
			var ev struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					Message string `json:"message"`
				} `json:"params"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(raw), &ev) == nil {
				// 进度通知：实时回调（不按 id 路由），实现流式。
				if ev.Method == "notifications/progress" {
					if ev.Params.Message != "" {
						t.mu.Lock()
						cb := t.onProgress
						t.mu.Unlock()
						if cb != nil {
							cb(ev.Params.Message)
						}
					}
					return
				}
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
			// 收到新的 event: 行时，先 flush 上一个尚未提交的事件（符合 SSE 规范：
			// 事件以空行分隔，但部分实现会在 data 后紧跟下一个 event:，需在此兜底）。
			flush()
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
	}, nil, nil); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := t.Notify("notifications/initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("initialized notify: %w", err)
	}
	var list struct {
		Tools []ToolMeta `json:"tools"`
	}
	if err := t.Call("tools/list", map[string]interface{}{}, &list, nil); err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	t.tools = list.Tools
	return nil
}

func (t *sseTransport) Tools() []ToolMeta { return t.tools }

func (t *sseTransport) postURL() string {
	endpoint := t.endpoint
	if endpoint == "" {
		// 服务端未推送 endpoint 时，按 MCP SSE 规范默认向 /messages 发送请求。
		endpoint = "/messages"
	}
	if strings.HasPrefix(endpoint, "http") {
		return endpoint
	}
	return joinURL(t.baseURL, endpoint)
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

func (t *sseTransport) Call(method string, params interface{}, out interface{}, onProgress func(message string)) error {
	if t.endpoint == "" {
		return fmt.Errorf("sse endpoint 尚未就绪")
	}
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	ch := make(chan sseResult, 1)
	t.pending[id] = ch
	t.onProgress = onProgress // 设置当前 Call 的进度回调（串行调用，安全）
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.onProgress = nil
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

// joinURL 把相对路径拼到 base 的 host 根目录下（而非拼到 base 的完整路径之后）。
// llama.cpp 等 SSE 服务在 /sse 端点下推送 endpoint: /messages?session_id=xxx（相对路径），
// 该消息端点应与 /sse 同级（即 http://host/messages），而不是 http://host/sse/messages。
// 因此相对路径需相对于 base 的“目录”（最后一个 / 之前的部分）拼接，而非直接追加到 base 末尾。
func joinURL(base, rel string) string {
	if strings.HasPrefix(rel, "http") {
		return rel
	}
	// 以 base 的最后一个 "/" 为界，取目录部分再拼接相对路径。
	// 例：base="http://127.0.0.1:8081/sse"，rel="/messages?x=1"
	//   -> "http://127.0.0.1:8081" + "/" + "messages?x=1"
	idx := strings.LastIndex(base, "/")
	if idx < 0 {
		return rel
	}
	dir := base[:idx]
	rel = strings.TrimPrefix(rel, "/")
	if dir == "" {
		return rel
	}
	if strings.HasSuffix(dir, "/") {
		return dir + rel
	}
	return dir + "/" + rel
}
