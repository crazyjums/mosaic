import { existsSync } from "node:fs";
import { dirname, join } from "node:path";

// 找到 pi SDK 的入口文件。
//
// 为什么不直接 import "@earendil-works/pi-coding-agent"：pi 的包自带一份打好包的 SDK（dist/bundle，9MB，只依赖 Node 内置模块），
// 安装包里只放这一份，不放它那 2 万多个依赖文件——否则 mac 通用包合并 asar 时会因为文件太多而失败，安装包也大很多。
// 打包版放在 resources/pi/（旁边有一个 {"type":"module"} 的 package.json，因为 bundle 是 ESM）；
// 开发和测试时从 node_modules 里找（pi 在 devDependencies 里，不进安装包）。

const REL = join("node_modules", "@earendil-works", "pi-coding-agent", "dist", "bundle", "index.js");

export function findPiEntry(startDir: string, resourcesPath?: string, exists: (p: string) => boolean = existsSync): string | undefined {
  if (resourcesPath) {
    const packaged = join(resourcesPath, "pi", "index.js");
    if (exists(packaged)) return packaged;
  }
  for (let dir = startDir; ; dir = dirname(dir)) {
    const p = join(dir, REL);
    if (exists(p)) return p;
    if (dirname(dir) === dir) return undefined;
  }
}
