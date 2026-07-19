// Package webui 提供数据分析助手的聊天前端页面（Vue 3 构建产物，嵌入编译）。
package webui

import "embed"

// Assets 编译期内嵌的 Web 资源。
//
//go:embed all:dist
var Assets embed.FS
