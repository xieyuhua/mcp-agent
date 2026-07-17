package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"os"

	"company.com/mcp-data-server/internal/mcp"
)

// Stdio 基于标准输入/输出的传输层（MCP 默认传输）。
type Stdio struct {
	in      *bufio.Reader
	out     *bufio.Writer
	handler func(ctx context.Context, raw []byte) ([]byte, error)
}

func NewStdio(server *mcp.Server) *Stdio {
	return &Stdio{
		in:  bufio.NewReader(os.Stdin),
		out: bufio.NewWriter(os.Stdout),
		handler: func(ctx context.Context, raw []byte) ([]byte, error) {
			return server.Handle(ctx, raw)
		},
	}
}

// Run 循环读取一行 JSON-RPC 消息并写回响应。
func (s *Stdio) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := s.in.ReadBytes('\n')
		if len(line) > 0 {
			// 去掉首尾空白，并剥离可能的 UTF-8 BOM（部分客户端/管道会在流首添加）
			trimmed := stripBOM(trimSpace(line))
			if len(trimmed) > 0 {
				resp, herr := s.handler(ctx, trimmed)
				if herr != nil {
					// 协议层错误也应尝试返回
					if b, jerr := json.Marshal(map[string]interface{}{
						"jsonrpc": "2.0", "error": map[string]interface{}{"code": -32603, "message": herr.Error()},
					}); jerr == nil {
						s.write(b)
					}
					continue
				}
				if resp != nil {
					s.write(resp)
				}
			}
		}
		if err != nil {
			if err.Error() == "EOF" {
				return nil // 客户端断开，优雅退出
			}
			return err
		}
	}
}

func (s *Stdio) write(b []byte) {
	s.out.Write(b)
	s.out.WriteByte('\n')
	s.out.Flush()
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// stripBOM 去掉 UTF-8 字节顺序标记（EF BB BF）。
func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
