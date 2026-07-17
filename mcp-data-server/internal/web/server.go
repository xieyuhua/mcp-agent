package web

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// StaticHandler 返回静态资源处理器：
//   - 若 overrideDir 存在且为目录，优先从该目录加载（可分离部署，指定目录即可热更新前端）；
//   - 否则使用编译期内嵌资源（与 Golang 二进制打包在一起，单文件分发）。
//
// 同时支持单页应用（SPA）回退：未命中的前端路由统一返回 index.html。
func StaticHandler(overrideDir string) http.Handler {
	if overrideDir != "" {
		if info, err := os.Stat(overrideDir); err == nil && info.IsDir() {
			return spaFallback(overrideDir, http.FileServer(http.Dir(overrideDir)))
		}
	}
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic("embed assets: " + err.Error())
	}
	return spaFallback("", http.FileServer(http.FS(sub)))
}

// spaFallback 单页应用回退：未命中的路径返回 index.html。
// dir 为空表示内嵌模式，从 assetsFS 读取。
func spaFallback(dir string, fs http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if dir != "" {
			fp := filepath.Join(dir, filepath.Clean(p))
			if info, err := os.Stat(fp); err == nil && !info.IsDir() {
				fs.ServeHTTP(w, r)
				return
			}
		} else {
			clean := strings.TrimPrefix(p, "/")
			if clean == "" {
				clean = "index.html"
			}
			if _, err := assetsFS.Open("assets/" + clean); err == nil {
				fs.ServeHTTP(w, r)
				return
			}
		}
		// 回退到 index.html
		if dir != "" {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		data, err := assetsFS.ReadFile("assets/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
}
