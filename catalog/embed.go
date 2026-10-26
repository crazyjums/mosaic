package catalog

import "embed"

// FS 的结构：registry.json 和 bundled/<插件目录>。
//
//go:embed registry.json all:bundled
var FS embed.FS
