import type { HarnessManager } from "./harness";
import type { RunSpec } from "./workers/protocol";
import { installUpdate, defaultDeps, updatesDir, validateUpdatePath } from "./update.js";
import { BrowserWindow, dialog, shell, type FileFilter, type OpenDialogOptions } from "electron";

// 后端（mosaicd）通过 host.* 消息请壳做的事。参数格式见 docs/design/electron-migration.md §4.3，
// 和 Go 端 internal/app/host.go 里的选项一一对应。

interface Filter {
  display_name: string;
  pattern: string;
}

/** Wails 的过滤器写法是 "*.zip" 或 "*.png;*.jpg"；Electron 要扩展名数组（不带 * 和点）。 */
export function toElectronFilters(filters: Filter[] | undefined): FileFilter[] {
  return (filters ?? []).map((f) => ({
    name: f.display_name,
    extensions: f.pattern
      .split(";")
      .map((p) => p.trim().replace(/^\*\.?/, ""))
      .map((e) => e || "*"),
  }));
}

/** 只放行 http / https：后端的一个 bug、或者模型输出里的链接，都不能让壳去打开 file:// 或自定义协议。 */
export function isWebURL(raw: unknown): raw is string {
  if (typeof raw !== "string") return false;
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

export function openExternal(raw: unknown): void {
  if (isWebURL(raw)) void shell.openExternal(raw);
}

export interface HostDeps {
  window: () => BrowserWindow | undefined;
  quit: () => void;
  /** 引擎管理器（Claude Code、pi）。没有时 host.harness.* 报错。 */
  harness?: () => HarnessManager | undefined;
}

/** 校验 Go 发来的一轮请求：来自我们自己的后端，但 worker 会拿它启动进程，所以仍然逐项检查。 */
export function parseRunSpec(raw: unknown): RunSpec {
  const p = (raw ?? {}) as Record<string, unknown>;
  const str = (k: string, required = true): string => {
    const v = p[k];
    if (typeof v === "string" && (v !== "" || !required)) return v;
    if (!required && v === undefined) return "";
    throw new Error(`引擎请求的 ${k} 不合法`);
  };
  const options = p.options && typeof p.options === "object" && !Array.isArray(p.options) ? (p.options as Record<string, unknown>) : undefined;
  return {
    engine: str("engine"),
    session_id: str("session_id"),
    profile: str("profile", false) || undefined,
    text: str("text", false),
    workspace: str("workspace"),
    model: str("model", false) || undefined,
    permission_mode: str("permission_mode"),
    options,
    resume: str("resume", false) || undefined,
  };
}

export function createHostHandler({ window, quit, harness }: HostDeps) {
  return async (method: string, params: unknown): Promise<unknown> => {
    const p = (params ?? {}) as Record<string, unknown>;
    const win = window();
    switch (method) {
      case "host.openURL":
        openExternal(p.url);
        return null;
      case "host.quit":
        quit();
        return null;
      case "host.harness.run": {
        const mgr = harness?.();
        if (!mgr) throw new Error("壳没有启用引擎管理");
        if (typeof p.run_id !== "string" || !p.run_id) throw new Error("缺少 run_id");
        return mgr.run(p.run_id, parseRunSpec(p.spec));
      }
      case "host.harness.cancel":
        if (typeof p.run_id === "string") harness?.()?.cancel(p.run_id);
        return null;
      case "host.installUpdate": {
        const path = validateUpdatePath(p.path, updatesDir(), process.platform);
        installUpdate(path, defaultDeps(quit));
        return null;
      }
      case "host.openFile": {
        const r = await run(win, { title: str(p.title), defaultPath: str(p.default_directory), filters: toElectronFilters(p.filters as Filter[]), properties: ["openFile" as const] });
        return r.canceled ? "" : (r.filePaths[0] ?? "");
      }
      case "host.openDirectory": {
        const props: ("openDirectory" | "createDirectory")[] = p.can_create_directories ? ["openDirectory", "createDirectory"] : ["openDirectory"];
        const r = await run(win, { title: str(p.title), defaultPath: str(p.default_directory), properties: props });
        return r.canceled ? "" : (r.filePaths[0] ?? "");
      }
      case "host.saveFile": {
        const opts = { title: str(p.title), defaultPath: str(p.default_filename), filters: toElectronFilters(p.filters as Filter[]) };
        const r = win ? await dialog.showSaveDialog(win, opts) : await dialog.showSaveDialog(opts);
        return r.canceled ? "" : (r.filePath ?? "");
      }
      default:
        throw new Error(`壳不支持 ${method}`);
    }
  };
}

function run(win: BrowserWindow | undefined, opts: OpenDialogOptions) {
  return win ? dialog.showOpenDialog(win, opts) : dialog.showOpenDialog(opts);
}

function str(v: unknown): string | undefined {
  return typeof v === "string" && v ? v : undefined;
}
