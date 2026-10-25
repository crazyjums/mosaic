// 客户端更新的状态：最近一次检查的结果、是否在检查、安装进度。
//
// 自动检查：启动 5 秒后一次，之后每 6 小时一次（设置 → 关于 里可以关闭）。自动检查失败时静默——
// 没网络、GitHub 打不开都很常见，不该弹错误打扰用户；手动检查失败才显示原因。

import { create } from "zustand";
import { backend, type UpdateInfo, type UpdateStatus } from "../bridge";
import { useUIPrefs } from "./uiPrefs";

interface UpdateState {
  version: string;
  info: UpdateInfo | null;
  checking: boolean;
  /** 手动检查的错误；自动检查不设置它 */
  error: string;
  status: UpdateStatus;
  /** 更新窗口是否打开 */
  dialogOpen: boolean;
  /** 本次运行中用户已经看过更新内容的版本：看过之后按钮不再闪烁（下次启动或有更新的版本再闪） */
  seen: string;
  init(): Promise<void>;
  check(manual: boolean): Promise<void>;
  install(): Promise<void>;
  /** 打开更新窗口；没有新版本时顺便检查一次 */
  openDialog(): void;
  /** 关闭更新窗口（"稍后"） */
  closeDialog(): void;
  skip(): void;
}

const IDLE: UpdateStatus = { state: "idle", done: 0, total: 0 };
const RECHECK_MS = 6 * 60 * 60 * 1000;

let started = false;
let poller: ReturnType<typeof setInterval> | undefined;

export const useUpdate = create<UpdateState>((set, get) => ({
  version: "",
  info: null,
  checking: false,
  error: "",
  status: IDLE,
  dialogOpen: false,
  seen: "",

  init: async () => {
    if (started) return;
    started = true;
    try {
      set({ version: await backend().getVersion() });
    } catch {
      // 版本号只用于显示
    }
    const auto = () => {
      if (useUIPrefs.getState().prefs.updateCheck === "auto") void get().check(false);
    };
    setTimeout(auto, 5000);
    setInterval(auto, RECHECK_MS);
  },

  check: async (manual) => {
    if (get().checking) return;
    set({ checking: true, error: manual ? "" : get().error });
    try {
      const info = await backend().checkUpdate();
      set({ info, error: "" });
    } catch (e) {
      if (manual) set({ error: String(e instanceof Error ? e.message : e) });
    } finally {
      set({ checking: false });
    }
  },

  install: async () => {
    set({ error: "", status: { state: "downloading", done: 0, total: get().info?.size ?? 0 } });
    try {
      await backend().installUpdate();
    } catch (e) {
      set({ status: { state: "error", done: 0, total: 0, error: String(e instanceof Error ? e.message : e) } });
      return;
    }
    // 下载在后台进行，轮询进度；成功后应用会自己重启，窗口随之关闭。
    clearInterval(poller);
    poller = setInterval(async () => {
      try {
        const st = await backend().getUpdateStatus();
        set({ status: st });
        if (st.state === "error" || st.state === "idle") clearInterval(poller);
      } catch {
        // 应用退出重启时连接断开是正常的
      }
    }, 500);
  },

  openDialog: () => {
    const { info } = get();
    set({ dialogOpen: true, error: "", seen: info?.available ? info.latest : get().seen });
    if (!info?.available) void get().check(true);
  },
  closeDialog: () => set({ dialogOpen: false }),
  skip: () => {
    const v = get().info?.latest;
    if (v) void useUIPrefs.getState().update({ skippedVersion: v });
    set({ dialogOpen: false });
  },
}));

/**
 * 右上角更新按钮的状态：
 *   busy   —— 正在下载 / 安装；
 *   blink  —— 有新版本，用户还没看过更新内容：高亮并闪烁，提醒有更新；
 *   steady —— 有新版本但这次运行里已经看过（点了"稍后"）：保持高亮，不再闪；
 *   none   —— 没有新版本，或者用户选了"跳过此版本"：只是一个普通的"检查更新"入口。
 */
export type UpdateIndicator = "none" | "blink" | "steady" | "busy";

export function updateIndicator(s: Pick<UpdateState, "info" | "seen" | "status">, skipped: string): UpdateIndicator {
  if (s.status.state !== "idle" && s.status.state !== "error") return "busy";
  if (!s.info?.available || s.info.latest === skipped) return "none";
  return s.info.latest === s.seen ? "steady" : "blink";
}

export function fmtMB(n: number): string {
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
