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

// Client 是一个 MCP stdio 客户端：拉起 mcp-data-server 子进程，
// 通过换行分隔的 JSON-RPC 与之通信。
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	nextID int

	tools   []ToolMeta
	toolSet map[string]bool
}

// StartConfig 启动子进程所需参数。
type StartConfig struct {
	ServerPath string
	DBDialect  string
	DBDsn      string
	Env        map[string]string
	MaskEnabled bool
	SeedDemo   bool
}

// Start 拉起 MCP 子进程并完成 initialize 握手。
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

	c := &Client{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		toolSet: map[string]bool{},
	}

	// initialize 握手（无需解析返回体）
	if err := c.call("initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "data-analysis-agent", "version": "1.0.0"},
	}, nil); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	// 发送 initialized 通知（无需响应）
	_ = c.notify("notifications/initialized", map[string]interface{}{})

	// 拉取工具清单
	var list struct {
		Tools []ToolMeta `json:"tools"`
	}
	if err := c.call("tools/list", map[string]interface{}{}, &list); err != nil {
		c.Close()
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	c.tools = list.Tools
	for _, t := range c.tools {
		c.toolSet[t.Name] = true
	}
	return c, nil
}

// Tools 返回工具清单。
func (c *Client) Tools() []ToolMeta { return c.tools }

// HasTool 是否存在某工具。
func (c *Client) HasTool(name string) bool { return c.toolSet[name] }

// CallTool 调用一个 MCP 工具，返回文本结果；isError 指示工具执行是否报错。
func (c *Client) CallTool(name string, args map[string]interface{}) (text string, isError bool, err error) {
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.call("tools/call", map[string]interface{}{
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

// call 发送一个请求并等待匹配 id 的响应。
func (c *Client) call(method string, params interface{}, out interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	id := c.nextID
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
	if _, err := c.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	// 读取直到拿到匹配 id 的响应
	for {
		line, err := c.stdout.ReadBytes('\n')
		if len(line) > 0 {
			var resp struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if jerr := json.Unmarshal(line, &resp); jerr == nil && resp.ID != nil {
				// 校验 id 匹配
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

// notify 发送一个通知（无 id，无响应）。
func (c *Client) notify(method string, params interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// Close 关闭客户端并终止子进程。
func (c *Client) Close() {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}
