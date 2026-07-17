package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveWorkPath 把相对路径解析到沙箱内的绝对路径，并校验不越界。
// 所有文件工具都必须经过它，禁止通过 ../ 等访问工作目录之外。
func (h *ToolHandler) resolveWorkPath(rel string) (string, error) {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "/")
	// 先按段检查，任何显式 .. 都直接拒绝（防逃逸）。
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", fmt.Errorf("非法路径，禁止访问沙箱之外的位置: %s", rel)
		}
	}
	// 规范化并拦截任何企图逃出沙箱的路径。
	clean := filepath.Clean("/" + rel)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("非法路径，禁止访问沙箱之外的位置: %s", rel)
	}
	root := h.workDir
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析工作目录失败: %w", err)
	}
	target := filepath.Join(abs, clean)
	// 二次校验：目标必须以 root 为前缀。
	if target != abs && !strings.HasPrefix(target, abs+string(os.PathSeparator)) {
		return "", fmt.Errorf("非法路径，禁止访问沙箱之外的位置: %s", rel)
	}
	return target, nil
}

func (h *ToolHandler) readFile(args map[string]interface{}) (interface{}, error) {
	if _, _, err := h.ctxFromArgs(args); err != nil {
		return nil, err
	}
	rel, _ := args["path"].(string)
	if rel == "" {
		return nil, fmt.Errorf("path is required")
	}
	maxBytes := optInt(args["max_bytes"])
	if maxBytes <= 0 {
		maxBytes = 65536
	}
	if maxBytes > 1048576 {
		maxBytes = 1048576
	}
	p, err := h.resolveWorkPath(rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	buf := make([]byte, maxBytes)
	n, err := f.Read(buf)
	if err != nil && err.Error() != "EOF" {
		// 读满 maxBytes 时 Read 会返回 io.EOF，属正常；其余错误上报
		if !strings.Contains(err.Error(), "EOF") {
			return nil, fmt.Errorf("read file: %w", err)
		}
	}
	return map[string]interface{}{
		"path":     rel,
		"bytes":    n,
		"truncated": n >= maxBytes,
		"content":  string(buf[:n]),
	}, nil
}

func (h *ToolHandler) writeFile(args map[string]interface{}) (interface{}, error) {
	if _, _, err := h.ctxFromArgs(args); err != nil {
		return nil, err
	}
	rel, _ := args["path"].(string)
	if rel == "" {
		return nil, fmt.Errorf("path is required")
	}
	content, _ := args["content"].(string)
	p, err := h.resolveWorkPath(rel)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir parent: %w", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}
	return map[string]interface{}{
		"path":    rel,
		"bytes":   len(content),
		"written": true,
	}, nil
}

func (h *ToolHandler) appendFile(args map[string]interface{}) (interface{}, error) {
	if _, _, err := h.ctxFromArgs(args); err != nil {
		return nil, err
	}
	rel, _ := args["path"].(string)
	if rel == "" {
		return nil, fmt.Errorf("path is required")
	}
	content, _ := args["content"].(string)
	p, err := h.resolveWorkPath(rel)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir parent: %w", err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	n, err := f.WriteString(content)
	if err != nil {
		return nil, fmt.Errorf("append file: %w", err)
	}
	return map[string]interface{}{
		"path":    rel,
		"bytes":   n,
		"appended": true,
	}, nil
}

func (h *ToolHandler) listDir(args map[string]interface{}) (interface{}, error) {
	if _, _, err := h.ctxFromArgs(args); err != nil {
		return nil, err
	}
	rel, _ := args["path"].(string)
	p, err := h.resolveWorkPath(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}
	items := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		item := map[string]interface{}{
			"name":  e.Name(),
			"is_dir": e.IsDir(),
		}
		if info != nil {
			item["size"] = info.Size()
			item["mod_time"] = info.ModTime().Format("2006-01-02 15:04:05")
		}
		items = append(items, item)
	}
	return map[string]interface{}{
		"path":  rel,
		"count": len(items),
		"items": items,
	}, nil
}

func (h *ToolHandler) makeDir(args map[string]interface{}) (interface{}, error) {
	if _, _, err := h.ctxFromArgs(args); err != nil {
		return nil, err
	}
	rel, _ := args["path"].(string)
	if rel == "" {
		return nil, fmt.Errorf("path is required")
	}
	p, err := h.resolveWorkPath(rel)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	return map[string]interface{}{
		"path":    rel,
		"created": true,
	}, nil
}

func (h *ToolHandler) deleteFile(args map[string]interface{}) (interface{}, error) {
	if _, _, err := h.ctxFromArgs(args); err != nil {
		return nil, err
	}
	rel, _ := args["path"].(string)
	if rel == "" {
		return nil, fmt.Errorf("path is required")
	}
	p, err := h.resolveWorkPath(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory, use delete_dir is not allowed; delete_file only removes files")
	}
	if err := os.Remove(p); err != nil {
		return nil, fmt.Errorf("remove: %w", err)
	}
	return map[string]interface{}{
		"path":    rel,
		"deleted": true,
	}, nil
}

func (h *ToolHandler) readDirTree(args map[string]interface{}) (interface{}, error) {
	if _, _, err := h.ctxFromArgs(args); err != nil {
		return nil, err
	}
	rel, _ := args["path"].(string)
	p, err := h.resolveWorkPath(rel)
	if err != nil {
		return nil, err
	}
	var nodes []map[string]interface{}
	var walk func(dir string, relPrefix string, depth int) error
	walk = func(dir string, relPrefix string, depth int) error {
		if depth > 2 {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			childRel := e.Name()
			if relPrefix != "" {
				childRel = relPrefix + "/" + e.Name()
			}
			node := map[string]interface{}{
				"path":   childRel,
				"is_dir": e.IsDir(),
			}
			nodes = append(nodes, node)
			if e.IsDir() {
				full := filepath.Join(dir, e.Name())
				if err := walk(full, childRel, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(p, rel, 0); err != nil {
		return nil, fmt.Errorf("walk dir: %w", err)
	}
	return map[string]interface{}{
		"root":  rel,
		"count": len(nodes),
		"tree":  nodes,
	}, nil
}
