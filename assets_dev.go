//go:build (desktop || dev || bindings) && !production

package main

import "embed"

// assets 在非 production 构建中为空：
//   - wails dev：页面由 Vite 开发服务器提供（wails.json 的 frontend:dev:serverUrl），不读嵌入资源；
//   - 生成绑定：只需要类型信息，不运行界面。
var assets embed.FS
