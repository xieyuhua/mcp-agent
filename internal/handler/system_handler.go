package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ---------- 系统信息工具 ----------

// systemInfo 收集当前运行环境的基础信息（跨平台，使用标准库）。
func (h *ToolHandler) systemInfo(args map[string]interface{}) (interface{}, error) {
	hostname, _ := os.Hostname()
	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	info := map[string]interface{}{
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"go_version":    runtime.Version(),
		"num_cpu":       runtime.NumCPU(),
		"num_goroutine": runtime.NumGoroutine(),
		"hostname":      hostname,
		"user":          user,
		"home_dir":      home,
		"work_dir":      wd,
	}
	switch runtime.GOOS {
	case "linux", "darwin":
		addUnixRelease(info)
	default:
		addWindowsRelease(info)
	}
	return info, nil
}

// systemDirs 识别并返回当前操作系统的标准系统目录（Windows / Linux 分别处理）。
func (h *ToolHandler) systemDirs(args map[string]interface{}) (interface{}, error) {
	home, _ := os.UserHomeDir()
	dirs := map[string]string{}

	switch runtime.GOOS {
	case "windows":
		systemRoot := os.Getenv("SystemRoot")
		if systemRoot == "" {
			systemRoot = "C:\\Windows"
		}
		dirs["system_root"] = systemRoot
		dirs["windows"] = systemRoot
		dirs["system32"] = filepath.Join(systemRoot, "System32")
		if v := os.Getenv("ProgramFiles"); v != "" {
			dirs["program_files"] = v
		}
		if v := os.Getenv("ProgramFiles(x86)"); v != "" {
			dirs["program_files_x86"] = v
		}
		if v := os.Getenv("ProgramData"); v != "" {
			dirs["program_data"] = v
		}
		if v := os.Getenv("USERPROFILE"); v != "" {
			dirs["user_profile"] = v
			dirs["desktop"] = filepath.Join(v, "Desktop")
			dirs["documents"] = filepath.Join(v, "Documents")
			dirs["downloads"] = filepath.Join(v, "Downloads")
			dirs["pictures"] = filepath.Join(v, "Pictures")
			dirs["music"] = filepath.Join(v, "Music")
			dirs["videos"] = filepath.Join(v, "Videos")
			dirs["app_data"] = filepath.Join(v, "AppData", "Roaming")
			dirs["local_app_data"] = filepath.Join(v, "AppData", "Local")
		}
		if v := os.Getenv("TEMP"); v != "" {
			dirs["temp"] = v
		}
	case "linux", "darwin":
		dirs["root"] = "/"
		dirs["etc"] = "/etc"
		dirs["var"] = "/var"
		dirs["tmp"] = "/tmp"
		dirs["usr"] = "/usr"
		dirs["bin"] = "/bin"
		dirs["opt"] = "/opt"
		dirs["proc"] = "/proc"
		if runtime.GOOS == "darwin" {
			dirs["applications"] = "/Applications"
			dirs["library"] = "/Library"
		}
		if home != "" {
			dirs["user_home"] = home
			dirs["desktop"] = filepath.Join(home, "Desktop")
			dirs["documents"] = filepath.Join(home, "Documents")
			dirs["downloads"] = filepath.Join(home, "Downloads")
			dirs["pictures"] = filepath.Join(home, "Pictures")
			dirs["music"] = filepath.Join(home, "Music")
			dirs["videos"] = filepath.Join(home, "Videos")
		}
	}

	items := make([]map[string]interface{}, 0, len(dirs))
	for k, v := range dirs {
		exists := false
		if v != "" {
			if _, err := os.Stat(v); err == nil {
				exists = true
			}
		}
		items = append(items, map[string]interface{}{"key": k, "path": v, "exists": exists})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["key"].(string) < items[j]["key"].(string)
	})
	return map[string]interface{}{"os": runtime.GOOS, "count": len(items), "dirs": items}, nil
}

// readSystemDir 读取任意目录（系统级，不受 work_dir 沙箱限制），用于浏览 Windows / Linux 系统目录。
func (h *ToolHandler) readSystemDir(args map[string]interface{}) (interface{}, error) {
	rel, _ := args["path"].(string)
	if strings.TrimSpace(rel) == "" {
		rel = "."
	}
	abs, err := filepath.Abs(rel)
	if err != nil {
		return nil, fmt.Errorf("解析路径失败: %w", err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("路径不是目录: %s", abs)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}
	maxEntries := optInt(args["max_entries"])
	if maxEntries <= 0 {
		maxEntries = 500
	}
	items := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		if len(items) >= maxEntries {
			break
		}
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
		"path":  abs,
		"count": len(items),
		"items": items,
	}, nil
}

// getEnv 读取环境变量。name 为空时返回全部（name->value）。
func (h *ToolHandler) getEnv(args map[string]interface{}) (interface{}, error) {
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) != "" {
		val := os.Getenv(name)
		return map[string]interface{}{
			"name":   name,
			"value":  val,
			"exists": val != "",
		}, nil
	}
	m := map[string]string{}
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i >= 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return map[string]interface{}{"count": len(m), "env": m}, nil
}

// diskUsage 查询指定路径所在磁盘的使用情况（跨平台）。
func (h *ToolHandler) diskUsage(args map[string]interface{}) (interface{}, error) {
	rel, _ := args["path"].(string)
	if strings.TrimSpace(rel) == "" {
		rel = "."
	}
	return diskUsageOf(rel)
}

// pct 计算使用率百分比。
func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

// ---------- 平台专属辅助 ----------

func addUnixRelease(info map[string]interface{}) {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		if runtime.GOOS == "darwin" {
			info["distro"] = "macOS"
		} else {
			info["distro"] = "unknown"
		}
		return
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		m[parts[0]] = strings.Trim(parts[1], `"`)
	}
	if name, ok := m["PRETTY_NAME"]; ok {
		info["distro"] = name
	} else {
		info["distro"] = m["NAME"]
	}
	info["distro_id"] = m["ID"]
	info["distro_version"] = m["VERSION_ID"]
}

func addWindowsRelease(info map[string]interface{}) {
	info["distro"] = os.Getenv("OS")
	info["computer_name"] = os.Getenv("COMPUTERNAME")
	info["processor_arch"] = os.Getenv("PROCESSOR_ARCHITECTURE")
	info["processor_count"] = os.Getenv("NUMBER_OF_PROCESSORS")
}
