import { existsSync } from "node:fs";
import { join } from "node:path";

export interface Paths {
  /** 后端可执行文件；为 null 时开发模式下用 `go run ./cmd/mosaicd`。 */
  mosaicd: string | null;
  /** 前端构建产物目录（里面有 index.html）。 */
  frontend: string;
  /** CHANGELOG.md（设置 → 关于里的版本历史）。 */
  changelog: string;
  /** 仓库根目录（开发模式才有）。 */
  repo: string | null;
}

/**
 * 找后端、前端和更新日志的位置。
 *   - 打包后：都在应用的 resources 目录下（electron-builder 的 extraResources，见阶段 2）；
 *   - 开发时：在仓库里。环境变量 MOSAIC_MOSAICD 可以指定后端程序。
 */
export function resolvePaths(opts: { packaged: boolean; resources: string; appDir: string; env: NodeJS.ProcessEnv }): Paths {
  const exe = process.platform === "win32" ? "mosaicd.exe" : "mosaicd";
  if (opts.packaged) {
    return { mosaicd: join(opts.resources, exe), frontend: join(opts.resources, "frontend"), changelog: join(opts.resources, "CHANGELOG.md"), repo: null };
  }
  const repo = join(opts.appDir, "..");
  const built = join(repo, "build", "bin", exe);
  const mosaicd = opts.env.MOSAIC_MOSAICD || (existsSync(built) ? built : null);
  return { mosaicd, frontend: join(repo, "frontend", "dist"), changelog: join(repo, "CHANGELOG.md"), repo };
}
