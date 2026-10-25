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
import { backend, type PermissionMode, type ProfileInfo, type SessionSummary, type Status, type WorkspaceInfo } from "../bridge";
import type { Decision } from "../protocol/events";
import { applyEvent, foldEvents, initialState, prependHistory, type ChatState } from "./reducer";
import { t } from "../i18n";

interface Store extends ChatState {
  backendKind: "wails" | "demo";
  profiles: ProfileInfo[];
  sessions: SessionSummary[];
  title: string;
  /** 当前会话实际使用的工作区（每个会话可以不同） */
  workspace: string;
  workspaceIsDefault: boolean;
  workspaceLocked: boolean;
  workspaceMissing: boolean;
  /** 工作区的 git 信息，状态栏用；定时刷新 */
  workspaceInfo: WorkspaceInfo | null;
  permissionMode: PermissionMode;
  /** 动作失败时显示在界面顶部的提示 */
  toast: string | null;
  /** 当前会话还有多少轮更早的历史没有加载（打开长会话时只加载最近几轮） */
  earlier: number;
  loadingEarlier: boolean;
  /** 每次把更早的历史接到前面后加一：消息列表据此保持滚动位置 */
  prependTick: number;

  init(): Promise<void>;
  send(text: string): Promise<void>;
  cancel(): Promise<void>;
  respondPermission(requestId: string, decision: Decision): Promise<void>;
  newSession(): Promise<void>;
  loadSession(id: string): Promise<void>;
  /** 加载当前会话更早的一页历史，放到已有内容前面 */
  loadEarlier(): Promise<void>;
  deleteSession(id: string): Promise<void>;
  /** 修改会话标题；失败时显示提示并返回 false */
  renameSession(id: string, title: string): Promise<boolean>;
  setModel(profile: string, model: string): Promise<void>;
  setPermissionMode(mode: PermissionMode): Promise<void>;
  /** 为当前会话选择工作区；传空字符串表示跟随默认工作区 */
  setSessionWorkspace(dir: string): Promise<void>;
  refreshWorkspace(): Promise<void>;
  /** 后端换了当前会话（例如清空历史后）：清空界面并切换到后端的当前会话 */
  resetToCurrentSession(): Promise<void>;
  /** 重新读取厂商列表、当前状态和会话列表（设置页修改厂商后调用） */
  refreshMeta(): Promise<void>;
  dismissToast(): void;
}

let initialized = false;

/** 从后端状态里取出工作区相关的字段（每个会话的工作区可以不同，切换会话时一起更新）。 */
const wsFrom = (st: Status) => ({
  workspace: st.workspace,
  workspaceIsDefault: st.workspace_is_default ?? true,
  workspaceLocked: st.workspace_locked ?? false,
  workspaceMissing: st.workspace_missing ?? false,
});

export const useChat = create<Store>((set, get) => {
  /** 包一层错误处理：任何动作失败都变成 toast，而不是未捕获的 Promise 异常。 */
  const guard = async (fn: () => Promise<void>) => {
    try {
      await fn();
    } catch (e) {
      set({ toast: String(e instanceof Error ? e.message : e) });
    }
  };

  // 运行中切换会话 / 新建会话：后端一次只跑一轮（运行中会返回 busy），所以先确认、停止当前这一轮，
  // 等后端真正停下来（最多几秒）再继续。返回 false 表示用户取消了。
  const stopRunning = async (): Promise<boolean> => {
    if (!get().running) return true;
    if (!confirm(t("当前会话还在运行。停止它并继续？"))) return false;
    await backend().cancel();
    for (let i = 0; i < 50; i++) {
      if (!(await backend().getStatus()).running) {
        set({ running: false });
        return true;
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    throw new Error(t("停止超时，请稍后再试"));
  };

  const refreshMeta = async () => {
    const b = backend();
    const [st, sessions, profiles] = await Promise.all([b.getStatus(), b.listSessions(), b.getProfiles()]);
    set({
      title: st.title, provider: st.provider, model: st.model, sessionUsage: st.usage, sessions, profiles,
      ...wsFrom(st), permissionMode: st.permission_mode,
    });
  };

  return {
    ...initialState(),
    backendKind: backend().kind,
    profiles: [],
    sessions: [],
    title: "",
    workspace: "",
    workspaceIsDefault: true,
    workspaceLocked: false,
    workspaceMissing: false,
    workspaceInfo: null,
    permissionMode: "default",
    toast: null,
    earlier: 0,
    loadingEarlier: false,
    prependTick: 0,

    async init() {
      if (initialized) return;
      initialized = true;
      const b = backend();

      // 所有事件都流经 reducer。一轮结束后刷新会话列表和状态栏数据。
      b.onEvent((ev) => {
        set((s) => applyEvent(s, ev));
        if (ev.type === "session_updated") {
          if (ev.session_id === get().sessionId) set({ title: ev.data.title });
          void refreshMeta(); // 会话列表里的标题也要更新（历史会话改名时也一样）
        }
        if (ev.type === "turn_end" && !ev.data.replay) {
          void refreshMeta();
          void get().refreshWorkspace(); // agent 可能改了文件，git 状态要更新
        }
      });
      // git 状态也可能被外部改变（用户在终端里提交），定时刷新一下。
      void get().refreshWorkspace();
      setInterval(() => void get().refreshWorkspace(), 15000);

      await guard(async () => {
        const [st, profiles, sessions] = await Promise.all([b.getStatus(), b.getProfiles(), b.listSessions()]);
        set({
          ...initialState(st.session_id),
          provider: st.provider,
          model: st.model,
          sessionUsage: st.usage,
          running: st.running,
          title: st.title,
          ...wsFrom(st),
          permissionMode: st.permission_mode,
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
        if (!(await stopRunning())) return;
        const st = await backend().newSession();
        set({ ...initialState(st.session_id), provider: st.provider, model: st.model, title: "", earlier: 0, loadingEarlier: false, ...wsFrom(st) });
        void get().refreshWorkspace();
      }),

    loadSession: (id) =>
      guard(async () => {
        if (!(await stopRunning())) return;
        // 先切换到目标会话再调用后端：这样加载期间旧会话残留的实时事件会被 reducer 丢弃。
        const prev = get();
        set({ ...initialState(id), provider: prev.provider, model: prev.model, earlier: 0, loadingEarlier: false });
        try {
          // 历史作为返回值一次性给出（只有最近几轮）：折叠完再 set 一次，界面只渲染一次。
          const r = await backend().loadSession(id);
          const st = r.status;
          set((s) => ({
            ...foldEvents(s, r.events),
            title: st.title, provider: st.provider, model: st.model, sessionUsage: st.usage, earlier: r.earlier,
            ...wsFrom(st),
          }));
          void get().refreshWorkspace(); // 不同会话的工作区不同，git 信息要重新取
        } catch (e) {
          // 加载失败时后端仍停留在原会话：恢复原来的界面状态，否则 sessionId 指向一个没加载成功的会话，
          // 之后原会话的所有事件都会被 reducer 过滤掉，界面再也不响应。
          set({
            sessionId: prev.sessionId,
            timeline: prev.timeline,
            tools: prev.tools,
            permissions: prev.permissions,
            sessionUsage: prev.sessionUsage,
            agents: prev.agents,
            tasks: prev.tasks,
            lastSeq: prev.lastSeq,
            earlier: prev.earlier,
          });
          throw e;
        }
      }),

    loadEarlier: () =>
      guard(async () => {
        const { sessionId, earlier, loadingEarlier } = get();
        if (!sessionId || earlier === 0 || loadingEarlier) return;
        set({ loadingEarlier: true });
        try {
          const page = await backend().loadEarlierHistory(sessionId);
          if (get().sessionId !== sessionId) return; // 等待期间用户切换了会话，这一页不属于现在的会话
          set((s) => ({ ...prependHistory(s, page.events), earlier: page.earlier, prependTick: s.prependTick + 1 }));
        } finally {
          if (get().sessionId === sessionId) set({ loadingEarlier: false });
        }
      }),

    deleteSession: (id) =>
      guard(async () => {
        await backend().deleteSession(id);
        // 删除的是当前会话时，后端已切换到一个新的空会话，界面跟着切换。
        if (id === get().sessionId) {
          const st = await backend().getStatus();
          set({ ...initialState(st.session_id), provider: st.provider, model: st.model, title: "", ...wsFrom(st) });
          void get().refreshWorkspace();
        }
        set({ sessions: await backend().listSessions() });
      }),

    renameSession: async (id, title) => {
      try {
        await backend().renameSession(id, title);
        return true;
      } catch (e) {
        set({ toast: String(e instanceof Error ? e.message : e) });
        return false;
      }
    },

    setModel: (profile, model) =>
      guard(async () => {
        const st = await backend().setModel(profile, model);
        set({ provider: st.provider, model: st.model });
      }),

    refreshMeta: () => guard(refreshMeta),

    setSessionWorkspace: (dir) =>
      guard(async () => {
        const st = await backend().setSessionWorkspace(dir);
        set(wsFrom(st));
        await Promise.all([get().refreshWorkspace(), refreshMeta()]);
      }),

    setPermissionMode: (mode) =>
      guard(async () => {
        const st = await backend().setPermissionMode(mode);
        set({ permissionMode: st.permission_mode });
      }),

    resetToCurrentSession: () =>
      guard(async () => {
        const st = await backend().getStatus();
        set({ ...initialState(st.session_id), provider: st.provider, model: st.model, title: "", sessionUsage: st.usage, earlier: 0, loadingEarlier: false, ...wsFrom(st) });
        await refreshMeta();
      }),

    async refreshWorkspace() {
      try {
        set({ workspaceInfo: await backend().getWorkspaceInfo() });
      } catch {
        // 读不到 git 信息只是少显示一项，不打扰用户
      }
    },
    dismissToast: () => set({ toast: null }),
  };
});
