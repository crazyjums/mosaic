//go:build desktop || dev || production || bindings

package main

import _ "embed"

// changelog 把 CHANGELOG.md 打包进程序，设置 → 关于里的"版本历史"直接用它，离线也能看。
// 放在根目录的 main 包里，是因为 go:embed 不能引用上级目录的文件。
//
//go:embed CHANGELOG.md
var changelog string
