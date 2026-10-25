//go:build production

package main

import "embed"

// assets 是打包进可执行文件的前端产物。
//
// 只在 production 构建（wails build）时嵌入。原因是 Wails 的构建顺序是
// "生成绑定（-tags bindings）→ 构建前端 → 编译（-tags desktop,production）"：
// 生成绑定那一步时 frontend/dist 还不存在，如果这里无条件 go:embed，
// 全新 clone 的仓库第一次 wails build 就会报 "pattern all:frontend/dist: no matching files found"。
//
//go:embed all:frontend/dist
var assets embed.FS
