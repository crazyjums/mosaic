import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

// 壳自己的设置（关窗行为、通知、全局快捷键）。它们属于桌面壳而不是 Go 后端：Wails 版没有这些概念，
// 所以单独存在 Electron 的 userData 目录里，不碰 ~/.myllmapp 的配置（见 docs/design/electron-migration.md）。

export type CloseBehavior = "tray-when-running" | "quit" | "tray";

export interface ShellSettings {
  /** 点关闭按钮时：有任务在跑才藏到托盘（默认）/ 总是退出 / 总是藏到托盘 */
  closeBehavior: CloseBehavior;
  /** 任务完成、需要确认时弹系统通知 */
  notifications: boolean;
  quickAsk: { enabled: boolean; shortcut: string };
}

export const DEFAULT_SHORTCUT = "CommandOrControl+Shift+M";

export function defaults(env: NodeJS.ProcessEnv = process.env): ShellSettings {
  // 环境变量 MOSAIC_QUICK_SHORTCUT 只影响默认值（空串 = 默认关闭）；用户在设置里改过就以设置为准。
  const fromEnv = env.MOSAIC_QUICK_SHORTCUT;
  return {
    closeBehavior: "tray-when-running",
    notifications: true,
    quickAsk: { enabled: fromEnv !== "", shortcut: fromEnv ? fromEnv : DEFAULT_SHORTCUT },
  };
}

const MODIFIERS = new Set(["Command", "Cmd", "Control", "Ctrl", "CommandOrControl", "CmdOrCtrl", "Alt", "Option", "AltGr", "Shift", "Super", "Meta"]);

/** 加速键格式：至少一个修饰键 + 一个按键，例如 CommandOrControl+Shift+M。具体能不能注册（被占用）要试了才知道。 */
export function validShortcut(s: unknown): s is string {
  if (typeof s !== "string" || s.length === 0 || s.length > 40 || !/^[A-Za-z0-9+]+$/.test(s)) return false;
  const parts = s.split("+");
  if (parts.length < 2 || parts.some((p) => p === "")) return false;
  const key = parts[parts.length - 1];
  const mods = parts.slice(0, -1);
  return mods.every((m) => MODIFIERS.has(m)) && !MODIFIERS.has(key);
}

/** 把任意输入（读出来的文件、前端传来的补丁）规整成合法设置：字段缺了或不合法就用 base 里的值。 */
export function normalize(raw: unknown, base: ShellSettings): ShellSettings {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const q = (r.quickAsk && typeof r.quickAsk === "object" ? r.quickAsk : {}) as Record<string, unknown>;
  return {
    closeBehavior: r.closeBehavior === "quit" || r.closeBehavior === "tray" || r.closeBehavior === "tray-when-running" ? r.closeBehavior : base.closeBehavior,
    notifications: typeof r.notifications === "boolean" ? r.notifications : base.notifications,
    quickAsk: {
      enabled: typeof q.enabled === "boolean" ? q.enabled : base.quickAsk.enabled,
      shortcut: validShortcut(q.shortcut) ? q.shortcut : base.quickAsk.shortcut,
    },
  };
}

export class SettingsFile {
  constructor(
    private readonly path: string,
    private readonly env: NodeJS.ProcessEnv = process.env,
  ) {}

  /** 文件不存在或损坏都回退到默认值：设置坏了不能让应用起不来。 */
  load(): ShellSettings {
    const base = defaults(this.env);
    try {
      return normalize(JSON.parse(readFileSync(this.path, "utf8")), base);
    } catch {
      return base;
    }
  }

  /** 先写临时文件再改名：中途崩溃不会留下写了一半的文件。 */
  save(s: ShellSettings): void {
    mkdirSync(dirname(this.path), { recursive: true });
    const tmp = `${this.path}.tmp`;
    writeFileSync(tmp, JSON.stringify(s, null, 2));
    renameSync(tmp, this.path);
  }
}
