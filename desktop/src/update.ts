import { spawn } from "node:child_process";
import { chmodSync, copyFileSync, renameSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";

// 安装后端下载好的更新包（迁移阶段 3，见 docs/design/electron-migration.md）。
//
// 信任边界：安装包是 Go 后端按“内置公钥签过的清单”里的 sha256 校验过的；壳只负责运行它。
// 所以壳只接受后端约定的下载目录里、后端命名规则的文件，不会去运行后端消息里任意指定的路径。

/** 和 Go 端对应：os.UserCacheDir()/mosaic/updates（internal 里 mosaicd 的 UseShellUpdater）。 */
export function updatesDir(env: NodeJS.ProcessEnv = process.env, platform: NodeJS.Platform = process.platform, home: string = homedir()): string {
  let cache: string;
  if (platform === "win32") cache = env.LOCALAPPDATA || join(home, "AppData", "Local");
  else if (platform === "darwin") cache = join(home, "Library", "Caches");
  else cache = env.XDG_CACHE_HOME || join(home, ".cache");
  return join(cache, "mosaic", "updates");
}

/** 路径必须直接在下载目录里，文件名是后端的 .myllmapp-update-* 命名，并且扩展名与平台相符。 */
export function validateUpdatePath(p: unknown, dir: string, platform: NodeJS.Platform): string {
  if (typeof p !== "string" || !p) throw new Error("更新包路径为空");
  const abs = resolve(p);
  if (dirname(abs) !== resolve(dir) || !basename(abs).startsWith(".myllmapp-update-")) {
    throw new Error("更新包不在约定的下载目录里，已拒绝");
  }
  if (platform === "win32" && !abs.toLowerCase().endsWith(".exe")) throw new Error("Windows 更新包必须是 .exe 安装程序");
  if (platform === "linux" && !abs.endsWith(".AppImage")) throw new Error("Linux 更新包必须是 AppImage");
  if (!statSync(abs).isFile()) throw new Error("更新包不是文件");
  return abs;
}

export interface InstallDeps {
  platform: NodeJS.Platform;
  env: NodeJS.ProcessEnv;
  /** 在后台启动一个不随本进程退出的程序。 */
  launch: (cmd: string, args: string[]) => void;
  quit: () => void;
}

export const defaultDeps = (quit: () => void): InstallDeps => ({
  platform: process.platform,
  env: process.env,
  launch: (cmd, args) => {
    const c = spawn(cmd, args, { detached: true, stdio: "ignore", windowsHide: true });
    c.on("error", () => undefined);
    c.unref();
  },
  quit,
});

/**
 * 运行已校验的安装包，然后退出本应用。
 *   - Windows：静默运行 NSIS 安装程序（装完自动启动新版）；
 *   - Linux：只有从 AppImage 运行时才能原地替换（改名覆盖，旧文件被占用也没关系），随后延迟几秒重启，等本进程释放数据库；
 *   - macOS：没有签名公证，不自动安装（后端本来就把它标成手动更新）。
 */
export function installUpdate(path: string, deps: InstallDeps): void {
  switch (deps.platform) {
    case "win32":
      deps.launch(path, ["/S", "--updated", "--force-run"]);
      break;
    case "linux": {
      const target = deps.env.APPIMAGE;
      if (!target) throw new Error("不是从 AppImage 运行的，无法原地更新，请手动下载安装");
      const staged = `${target}.new`;
      copyFileSync(path, staged);
      chmodSync(staged, 0o755);
      renameSync(staged, target);
      deps.launch("sh", ["-c", 'sleep 2; exec "$0"', target]);
      break;
    }
    default:
      throw new Error("这个平台需要手动下载安装");
  }
  deps.quit();
}
