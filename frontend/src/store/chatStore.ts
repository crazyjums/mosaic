// 全局状态（Zustand）。
//
// 职责划分：
//   - reducer.ts：事件 → 状态（纯函数，可测试）；
//   - 本文件：持有状态、订阅后端事件、提供"动作"（发送、确认权限、切换模型……）给组件调用；
//   - 组件：只读状态 + 调用动作，不碰事件，也不碰 Wails。
//
// 选 Zustand 而不是 Redux / Context：API 极小（一个 create），不需要 Provider 包裹，
// 组件用 selector 订阅局部状态，流式更新时只重渲染真正变化的部分。

import { create } from "zustand";
import { backend, type ProfileInfo, type SessionSummary } from "../bridge";
import type { Decision } from "../protocol/events";
import { applyEvent, initialState, type ChatState } from "./reducer";

interface Store extends ChatState {
  backendKind: "wails" | "demo";
  profiles: ProfileInfo[];
  sessions: SessionSummary[];
  title: string;
  workspace: string;
  /** 动作失败时显示在界面顶部的提示 */
  toast: string | null;

  init(): Promise<void>;
  send(text: string): Promise<void>;
  cancel(): Promise<void>;
  respondPermission(requestId: string, decision: Decision): Promise<void>;
  newSession(): Promise<void>;
  loadSession(id: string): Promise<void>;
  deleteSession(id: string): Promise<void>;
  setModel(profile: string, model: string): Promise<void>;
  dismissToast(): void;
}

let initialized = false;

export const useChat = create<Store>((set, get) => {
  /** 包一层错误处理：任何动作失败都变成 toast，而不是未捕获的 Promise 异常。 */
  const guard = async (fn: () => Promise<void>) => {
    try {
      await fn();
    } catch (e) {
      set({ toast: String(e instanceof Error ? e.message : e) });
    }
  };

  const refreshMeta = async () => {
    const b = backend();
    const [st, sessions] = await Promise.all([b.getStatus(), b.listSessions()]);
    set({ title: st.title, provider: st.provider, model: st.model, sessionUsage: st.usage, sessions });
  };

  return {
    ...initialState(),
    backendKind: backend().kind,
    profiles: [],
    sessions: [],
    title: "",
    workspace: "",
    toast: null,

    async init() {
      if (initialized) return;
      initialized = true;
      const b = backend();

      // 所有事件都流经 reducer。一轮结束后刷新会话列表和状态栏数据。
      b.onEvent((ev) => {
        set((s) => applyEvent(s, ev));
        if (ev.type === "turn_end" && !ev.data.replay) void refreshMeta();
      });

      await guard(async () => {
        const [st, profiles, sessions] = await Promise.all([b.getStatus(), b.getProfiles(), b.listSessions()]);
        set({
          ...initialState(st.session_id),
          provider: st.provider,
          model: st.model,
          sessionUsage: st.usage,
          running: st.running,
          title: st.title,
          workspace: st.workspace,
          profiles,
          sessions,
        });
      });
    },

    send: (text) => guard(() => backend().send(text)),
    cancel: () => guard(() => backend().cancel()),
    respondPermission: (id, decision) => guard(() => backend().respondPermission(id, decision)),

    newSession: () =>
      guard(async () => {
        const st = await backend().newSession();
        set({ ...initialState(st.session_id), provider: st.provider, model: st.model, title: "" });
      }),

    loadSession: (id) =>
      guard(async () => {
        if (get().running) throw new Error("当前一轮还在运行，请先停止");
        // 先切换到目标会话再调用后端：后端会在调用返回之前就开始推送重放事件，
        // 提前设置 sessionId 才能让 reducer 接收它们、同时丢弃旧会话的残留事件。
        const { provider, model } = get();
        set({ ...initialState(id), provider, model });
        const st = await backend().loadSession(id);
        set({ title: st.title, provider: st.provider, model: st.model, sessionUsage: st.usage });
      }),

    deleteSession: (id) =>
      guard(async () => {
        await backend().deleteSession(id);
        set({ sessions: await backend().listSessions() });
      }),

    setModel: (profile, model) =>
      guard(async () => {
        const st = await backend().setModel(profile, model);
        set({ provider: st.provider, model: st.model });
      }),

    dismissToast: () => set({ toast: null }),
  };
});
