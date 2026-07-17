package web

import "embed"

// assetsFS 编译期内嵌的 Web 资源（与 Golang 二进制打包在一起）。
//
//go:embed all:assets
var assetsFS embed.FS
