// Package admin 通过 go:embed 内嵌 Vue 管理后台的构建产物（dist），
// 使二进制可独立分发，无需额外部署静态文件。
//
// 构建方式：
//
//	cd web/admin && npm install && npm run build
//
// 若 dist 尚未构建，Assets() 返回 ok=false，服务端会给出友好提示而非启动失败。
package admin

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets 返回 dist 目录的子文件系统。
// 当未执行前端构建时（dist 内只有占位文件），ok 为 false。
func Assets() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	// 以 index.html 是否存在判断前端是否已构建
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
