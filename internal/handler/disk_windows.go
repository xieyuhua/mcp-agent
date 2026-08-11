//go:build windows

package handler

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// diskUsageOf 返回指定路径所在磁盘的空间使用情况（Windows）。
func diskUsageOf(path string) (map[string]interface{}, error) {
	var freeBytes, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(path), &freeBytes, &totalBytes, &totalFreeBytes); err != nil {
		return nil, fmt.Errorf("GetDiskFreeSpaceEx: %w", err)
	}
	used := totalBytes - totalFreeBytes
	return map[string]interface{}{
		"path":         path,
		"total_bytes":  totalBytes,
		"free_bytes":   totalFreeBytes,
		"used_bytes":   used,
		"used_percent": pct(used, totalBytes),
	}, nil
}
