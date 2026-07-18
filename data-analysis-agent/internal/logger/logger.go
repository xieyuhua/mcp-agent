// Package logger 提供带时间戳的全局日志器。
// 所有请求/环节的日志都经此输出：总是打印到控制台（stderr），
// 当 SaveToFile 开启时同时追加写入日志文件（按天滚动）。
package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger 带时间戳的日志器，可同时写控制台与文件。
type Logger struct {
	mu        sync.Mutex
	saveToFile bool
	dir       string
	file      *os.File // 当前打开的日志文件（按天）
	date      string   // 当前文件对应的日期串，用于按天滚动
	console   io.Writer
}

// 全局单例。
var std = &Logger{console: os.Stderr}

// Init 初始化全局日志器：根据 saveToFile 决定是否写文件，dir 为日志目录（空则用 ./logs）。
func Init(saveToFile bool, dir string) {
	std.mu.Lock()
	defer std.mu.Unlock()
	std.saveToFile = saveToFile
	if dir == "" {
		dir = "logs"
	}
	std.dir = dir
	// 关闭旧文件
	if std.file != nil {
		std.file.Close()
		std.file = nil
	}
	std.date = ""
	if saveToFile {
		_ = os.MkdirAll(dir, 0o755)
		_ = std.rotateLocked()
	}
}

// SetSaveToFile 运行时热更新是否写文件（不重启进程）。
func SetSaveToFile(saveToFile bool) {
	std.mu.Lock()
	defer std.mu.Unlock()
	if saveToFile == std.saveToFile {
		return
	}
	std.saveToFile = saveToFile
	if !saveToFile {
		if std.file != nil {
			std.file.Close()
			std.file = nil
		}
		std.date = ""
	}
}

// SetDir 运行时热更新日志目录（下次滚动生效）。
func SetDir(dir string) {
	std.mu.Lock()
	defer std.mu.Unlock()
	if dir == "" {
		dir = "logs"
	}
	std.dir = dir
	if std.saveToFile {
		_ = os.MkdirAll(dir, 0o755)
	}
}

// rotateLocked 打开（或滚动到）当天日志文件；调用方需持锁。
func (l *Logger) rotateLocked() error {
	today := time.Now().Format("2006-01-02")
	if l.file != nil && l.date == today {
		return nil
	}
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	path := filepath.Join(l.dir, "agent-"+today+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.file = f
	l.date = today
	return nil
}

// logf 输出一条带时间戳与级别的日志。
func logf(level, format string, args ...interface{}) {
	now := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] %s %s\n", now, level, msg)

	std.mu.Lock()
	// 控制台
	fmt.Fprint(std.console, line)
	// 文件
	if std.saveToFile {
		if err := std.rotateLocked(); err == nil && std.file != nil {
			std.file.WriteString(line)
		}
	}
	std.mu.Unlock()
}

// Infof 信息级日志（带时间戳）。
func Infof(format string, args ...interface{}) { logf("INFO ", format, args...) }

// Warnf 告警级日志（带时间戳）。
func Warnf(format string, args ...interface{}) { logf("WARN ", format, args...) }

// Errorf 错误级日志（带时间戳）。
func Errorf(format string, args ...interface{}) { logf("ERROR", format, args...) }

// Debugf 调试级日志（带时间戳）。
func Debugf(format string, args ...interface{}) { logf("DEBUG", format, args...) }

// Sanitize 去除可能含敏感信息的换行，便于单行记录。
func Sanitize(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ⏎ ")
}
