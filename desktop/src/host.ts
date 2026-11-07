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
}

export function createHostHandler({ window, quit }: HostDeps) {
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
