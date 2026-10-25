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
  /** 本次运行中用户点了"稍后"，横幅不再显示（下次启动或有更新的版本再提示） */
  dismissed: boolean;
  init(): Promise<void>;
  check(manual: boolean): Promise<void>;
  install(): Promise<void>;
  dismiss(): void;
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
  dismissed: false,

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
      set({ info, error: "", dismissed: info.latest !== get().info?.latest ? false : get().dismissed });
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

  dismiss: () => set({ dismissed: true }),
  skip: () => {
    const v = get().info?.latest;
    if (v) void useUIPrefs.getState().update({ skippedVersion: v });
  },
}));

/** 是否应该显示更新横幅：有新版本、没被跳过、本次没点"稍后"。正在安装时一直显示。 */
export function shouldShowBanner(s: Pick<UpdateState, "info" | "dismissed" | "status">, skipped: string): boolean {
  if (s.status.state !== "idle") return true;
  return !!s.info?.available && !s.dismissed && s.info.latest !== skipped;
}

export function fmtMB(n: number): string {
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
