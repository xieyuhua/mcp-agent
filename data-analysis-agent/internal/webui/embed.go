// Package webui 提供数据分析助手的聊天前端页面（自包含单文件，无需前端构建）。
// 通过 embed 内嵌 chat.html，由 server 在 `/` 与 `/ui` 路径提供。
package webui

import "embed"

// Assets 内嵌的前端页面资源。
//
//go:embed chat.html
var Assets embed.FS
