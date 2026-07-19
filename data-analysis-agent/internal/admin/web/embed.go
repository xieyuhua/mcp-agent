// Package web 内嵌后台管理页面的静态资源（与二进制打包在一起，无需独立构建）。
package web

import "embed"

// Assets 编译期内嵌的 Web 资源。
//
//go:embed all:dist
var Assets embed.FS
