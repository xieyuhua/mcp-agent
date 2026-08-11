//go:build !windows

package handler

import (
	"fmt"
	"syscall"
)

// diskUsageOf 返回指定路径所在文件系统的空间使用情况（Unix / macOS）。
func diskUsageOf(path string) (map[string]interface{}, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return nil, fmt.Errorf("statfs: %w", err)
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	used := total - free
	return map[string]interface{}{
		"path":         path,
		"total_bytes":  total,
		"free_bytes":   free,
		"used_bytes":   used,
		"used_percent": pct(used, total),
	}, nil
}
