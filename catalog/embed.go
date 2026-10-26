// Package catalog 是随应用一起发布的插件目录：内置的示例插件和默认的插件列表（registry.json）。
//
// 为什么放在单独的包里：go:embed 只能嵌入本包目录下的文件，而示例插件同时也是给插件作者看的文档，
// 需要放在仓库里显眼的位置；internal/plugin 通过 HostOptions.Catalog 接收它，自己不依赖具体内容。
//
// registry.json 同时也是线上的插件列表：应用会定时从仓库的这个文件刷新（见 plugin.HostOptions.RegistryURL），
// 刷新失败或离线时用本包嵌入的这一份。
package catalog

import "embed"

// FS 的结构：registry.json 和 bundled/<插件目录>。
//
//go:embed registry.json all:bundled
var FS embed.FS
