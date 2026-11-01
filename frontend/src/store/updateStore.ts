// 客户端更新的状态：最近一次检查的结果、是否在检查、安装进度。
//
// 自动检查：偏好读取完成后启动检查，成功后每小时检查；失败 5 分钟后重试。
// 窗口重新获得焦点、从后台恢复或网络恢复时，按限流间隔补检。失败不打断用户，
// 但会留下可见的诊断状态，避免网络问题让更新入口永久停在“检查更新”。

import { create } from "zustand";
import { backend, type UpdateInfo, type UpdateStatus } from "../bridge";
import { useUIPrefs } from "./uiPrefs";

interface UpdateState {
  version: string;
  info: UpdateInfo | null;
  checking: boolean;
  /** 手动检查失败只在更新弹窗中提示，后台检查失败不弹窗 */
  error: string;
  /** 自动检查失败的原因（供按钮 tooltip 与关于页诊断） */
  autoError: string;
  /** 最近一次发起检查的时刻，用于焦点/网络恢复时限流 */
  lastAttemptAt: number;
  /** 最近一次成功检查的时刻 */
  lastCheckedAt: number;
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
const STARTUP_DELAY_MS = 3000;
const RECHECK_MS = 60 * 60 * 1000;
const RETRY_MS = 5 * 60 * 1000;
const WAKE_RECHECK_MS = 10 * 60 * 1000;
const ONLINE_RECHECK_MS = 60 * 1000;

let started = false;
let autoTimer: ReturnType<typeof setTimeout> | undefined;
let poller: ReturnType<typeof setInterval> | undefined;

function scheduleAutoCheck(delay: number) {
  clearTimeout(autoTimer);
  if (useUIPrefs.getState().prefs.updateCheck !== "auto") return;
  autoTimer = setTimeout(() => {
    if (useUIPrefs.getState().prefs.updateCheck === "auto") void useUpdate.getState().check(false);
  }, delay);
}

/** 防止桌面窗口反复获得焦点时不停请求 GitHub；恢复网络可以更早重试。 */
export function shouldRecheck(lastAttemptAt: number, now: number, minimumInterval: number): boolean {
  return lastAttemptAt === 0 || now - lastAttemptAt >= minimumInterval;
}

function checkOnResume(minimumInterval: number) {
  if (useUIPrefs.getState().prefs.updateCheck !== "auto") return;
  const state = useUpdate.getState();
  if (!state.checking && !state.info?.disabled && shouldRecheck(state.lastAttemptAt, Date.now(), minimumInterval)) {
    void state.check(false);
  }
}

export const useUpdate = create<UpdateState>((set, get) => ({
  version: "",
  info: null,
  checking: false,
  error: "",
  autoError: "",
  lastAttemptAt: 0,
  lastCheckedAt: 0,
  status: IDLE,
  dialogOpen: false,
  seen: "",

  init: async () => {
    if (started) return;
    started = true;
    // App 先完成 useUIPrefs.load() 再调用 init，避免用户关闭自动检查时仍发起请求。
    scheduleAutoCheck(STARTUP_DELAY_MS);
    if (typeof window !== "undefined") {
      window.addEventListener("focus", () => checkOnResume(WAKE_RECHECK_MS));
      window.addEventListener("online", () => checkOnResume(ONLINE_RECHECK_MS));
      document.addEventListener("visibilitychange", () => {
        if (!document.hidden) checkOnResume(WAKE_RECHECK_MS);
      });
    }
    // 用户从“只手动”切回“自动”时立即检查，而不是等待下一个小时。
    useUIPrefs.subscribe((next, previous) => {
      if (next.prefs.updateCheck === previous.prefs.updateCheck) return;
      if (next.prefs.updateCheck === "auto") scheduleAutoCheck(0);
      else clearTimeout(autoTimer);
    });
    try {
      set({ version: await backend().getVersion() });
    } catch {
      // 版本号只用于显示
    }
  },

  check: async (manual) => {
    if (get().checking) return;
    set({ checking: true, lastAttemptAt: Date.now(), error: manual ? "" : get().error });
    let failed = false;
    try {
      const info = await backend().checkUpdate();
      set({ info, error: "", autoError: "", lastCheckedAt: Date.now() });
    } catch (e) {
      failed = true;
      const reason = String(e instanceof Error ? e.message : e);
      if (manual) set({ error: reason });
      else set({ autoError: reason });
    } finally {
      set({ checking: false });
      // 失败后较快重试；成功后每小时检查一次。已有已验证更新可继续保持高亮。
      if (started && useUIPrefs.getState().prefs.updateCheck === "auto" && !get().info?.disabled) {
        scheduleAutoCheck(failed ? RETRY_MS : RECHECK_MS);
      }
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
