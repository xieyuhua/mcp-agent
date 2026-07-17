package mcpclient

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// ToolMeta MCP 工具元信息（tools/list 返回）。
type ToolMeta struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// Transport MCP 传输层抽象。stdio 本地子进程与 http 远程均实现该接口。
type Transport interface {
	// Initialize 完成握手：initialize + initialized 通知 + tools/list。
	Initialize() error
	Notify(method string, params interface{}) error
	Call(method string, params interface{}, out interface{}) error
	Close() error
	// Tools 返回已发现的工具清单（Initialize 之后可用）。
	Tools() []ToolMeta
}

// Client 对外暴露的 MCP 客户端，内部委托给具体 Transport。
type Client struct {
	t Transport
}

// Tools 返回工具清单。
func (c *Client) Tools() []ToolMeta { return c.t.Tools() }

// HasTool 是否存在某工具。
func (c *Client) HasTool(name string) bool {
	for _, t := range c.t.Tools() {
		if t.Name == name {
			return true
		}
	}
	return false
}

// CallTool 调用一个 MCP 工具，返回文本结果；isError 指示工具执行是否报错。
func (c *Client) CallTool(name string, args map[string]interface{}) (text string, isError bool, err error) {
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.t.Call("tools/call", map[string]interface{}{
		"name":      name,
		"arguments": args,
	}, &out); err != nil {
		return "", false, err
	}
	var sb string
	for _, c0 := range out.Content {
		if c0.Type == "text" {
			sb += c0.Text
		}
	}
	return sb, out.IsError, nil
}

// Close 关闭底层 Transport。
func (c *Client) Close() { c.t.Close() }

// ---- stdio 传输（本地子进程）----

type stdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	mu      sync.Mutex
	nextID  int
	tools   []ToolMeta
}

// StartConfig 启动本地子进程所需参数。
type StartConfig struct {
	ServerPath  string
	DBDialect   string
	DBDsn       string
	Env         map[string]string
	MaskEnabled bool
	SeedDemo    bool
}

// Start 拉起本地 MCP 子进程（内置 mcp-data-server）并完成握手。
func Start(cfg StartConfig) (*Client, error) {
	if cfg.ServerPath == "" {
		return nil, fmt.Errorf("mcp server path is empty")
	}
	abs, err := filepath.Abs(cfg.ServerPath)
	if err != nil {
		abs = cfg.ServerPath
	}
	cmd := exec.Command(abs)
	cmd.Dir = filepath.Dir(abs)

	// 组装子进程环境变量（继承父进程 + 覆盖）
	env := os.Environ()
	put := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range cfg.Env {
		put(k, v)
	}
	put("DB_DIALECT", cfg.DBDialect)
	put("DB_DSN", cfg.DBDsn)
	// 作为本地子进程时强制 stdio 传输（忽略被调方配置文件里的 http/both）。
	put("TRANSPORT", "stdio")
	if cfg.MaskEnabled {
		put("MASK_ENABLED", "true")
	} else {
		put("MASK_ENABLED", "false")
	}
	if cfg.SeedDemo {
		put("SEED_DEMO", "true")
	} else {
		put("SEED_DEMO", "false")
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr // 子进程日志直接打到终端，便于排错

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp server: %w", err)
	}

	c := &stdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}
	if err := c.Initialize(); err != nil {
		c.Close()
		return nil, err
	}
	return &Client{t: c}, nil
}

func (t *stdioTransport) Initialize() error {
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

func (t *stdioTransport) Tools() []ToolMeta { return t.tools }

func (t *stdioTransport) Notify(method string, params interface{}) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = t.stdin.Write(append(b, '\n'))
	return err
}

func (t *stdioTransport) Call(method string, params interface{}, out interface{}) error {
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	t.mu.Unlock()

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := t.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	for {
		line, err := t.stdout.ReadBytes('\n')
		if len(line) > 0 {
			var resp struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if jerr := json.Unmarshal(line, &resp); jerr == nil && resp.ID != nil {
				var got int
				if uerr := json.Unmarshal(resp.ID, &got); uerr == nil && got == id {
					if resp.Error != nil {
						return fmt.Errorf("rpc error: %s", resp.Error.Message)
					}
					if out != nil && len(resp.Result) > 0 {
						if merr := json.Unmarshal(resp.Result, out); merr != nil {
							return fmt.Errorf("decode result: %w", merr)
						}
					}
					return nil
				}
			}
		}
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}
	}
}

func (t *stdioTransport) Close() error {
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return nil
}
