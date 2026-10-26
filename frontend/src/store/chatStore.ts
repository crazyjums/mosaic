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
import { backend, type Attachment, type PermissionMode, type ProfileInfo, type SessionSummary, type Status, type WorkspaceInfo } from "../bridge";
import type { Decision } from "../protocol/events";
import { applyEvent, foldEvents, initialState, prependHistory, type ChatState } from "./reducer";
import { t } from "../i18n";
import * as Q from "./queue";
import { applyActivity, reconcileRunning, type SessionActivity } from "./activity";

/** 离开一个会话时它的完整界面状态（只为"还在运行或有待发送消息"的会话保存），后台事件继续往里累积。 */
export type Stashed = ChatState & { earlier: number; title: string; paged: boolean };

interface Store extends ChatState, Q.QueueState {
  /** 后台会话的界面状态：多个会话可以同时运行，切走时它们继续跑，事件累积在这里，切回来原样恢复 */
  stash: Record<string, Stashed>;
  /** 非当前会话的运行情况（运行中 / 待确认 / 有新结果），会话列表据此显示标记 */
  activity: Record<string, SessionActivity>;
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
  /** 发送一条消息（可带附件）。运行中或队列里还有待发送的消息时先排队，见 store/queue.ts */
  send(text: string, attachments?: Attachment[]): Promise<void>;
  /** 重试发送失败的排队消息 */
  retryQueued(id: string): Promise<void>;
  removeQueued(id: string): void;
  /** 把一条待发送的消息移到队列最前面（只调整顺序，不会中断正在运行的这一轮） */
  moveQueuedFirst(id: string): void;
  /** 暂停之后继续发送队列 */
  resumeQueue(): Promise<void>;
  /** 取消当前会话正在运行的一轮（其他会话不受影响） */
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

/** 对象去掉一个键（不修改原对象）。 */
const omit = <T,>(o: Record<string, T>, k: string): Record<string, T> => {
  const { [k]: _drop, ...rest } = o;
  void _drop;
  return rest;
};

/** 从整个 store 里取出会话界面状态（ChatState 的字段）。 */
const VIEW_KEYS: (keyof ChatState)[] = ["sessionId", "timeline", "tools", "permissions", "running", "provider", "model", "sessionUsage", "agents", "tasks", "lastSeq", "versionWarning", "turnStartedAt", "lastTurnMs"];
const pickView = (s: ChatState): ChatState => Object.fromEntries(VIEW_KEYS.map((k) => [k, s[k]])) as unknown as ChatState;

const withActivity = (act: Record<string, SessionActivity>, sid: string, ev: Parameters<typeof applyActivity>[1]) => {
  const next = applyActivity(act[sid], ev);
  return next === act[sid] ? act : next ? { ...act, [sid]: next } : act;
};

export const useChat = create<Store>((set, get) => {
  /** 包一层错误处理：任何动作失败都变成 toast，而不是未捕获的 Promise 异常。 */
  const guard = async (fn: () => Promise<void>) => {
    try {
      await fn();
    } catch (e) {
      set({ toast: String(e instanceof Error ? e.message : e) });
    }
  };

  // 切换会话不再要求停止当前会话：多个会话可以同时运行，离开的会话转入后台继续跑。
  // 离开时如果它还在运行、有待确认的权限或待发送的消息，把完整的界面状态存进 stash，之后的事件继续累积到里面。
  const leaveCurrent = () => {
    const s = get();
    const sid = s.sessionId;
    if (!sid) return;
    const keep = s.running || s.permissions.length > 0 || Q.countFor(s, sid) > 0;
    const { [sid]: _drop, ...restStash } = s.stash;
    const { [sid]: _a, ...restAct } = s.activity;
    if (!keep) return set({ stash: restStash, activity: restAct });
    const { stash: _s, activity: _ac, ...rest } = s;
    void _s; void _ac;
    const view = pickView(rest as unknown as ChatState);
    set({
      stash: { ...restStash, [sid]: { ...view, earlier: s.earlier, title: s.title, paged: s.prependTick > 0 } },
      activity: { ...restAct, [sid]: { running: s.running, waiting: s.permissions.length, unread: false } },
    });
  };

  /** 进入一个会话时丢掉它的后台记录（stash 在调用方取走用于恢复） */
  const enter = (id: string) => {
    const { [id]: _drop, ...restStash } = get().stash;
    const { [id]: _a, ...restAct } = get().activity;
    set({ stash: restStash, activity: restAct });
  };

  /**
   * 发送队列的出口：取出某个会话该发的下一条，交给后端。
   * 关键：先同步地把它标成"发送中"再 await，这样重复的 turn_end 事件、连续的入队调用再进来时，
   * nextToSend 看到有"发送中"的就不会再发（不会重复发送、也不会乱序）。
   * 后台会话的队列同样会在它跑完一轮后接着发——用户此时可能正看着别的会话。
   */
  const drain = async (sid: string | null = get().sessionId) => {
    const s = get();
    if (!sid) return;
    const running = sid === s.sessionId ? s.running : (s.activity[sid]?.running ?? false);
    const next = Q.nextToSend(s, sid, running);
    if (!next) return;
    set(Q.markSending(s, next.id));
    try {
      await backend().send(sid, next.text, next.attachments.length ? next.attachments : undefined);
    } catch (e) {
      set((cur) => Q.markFailed(cur, next.id, String(e instanceof Error ? e.message : e)));
    }
  };

  const refreshMeta = async () => {
    const b = backend();
    const [st, sessions, profiles] = await Promise.all([b.getStatus(), b.listSessions(), b.getProfiles()]);
    set((cur) => ({
      title: st.title, provider: st.provider, model: st.model, sessionUsage: st.usage, sessions, profiles,
      ...wsFrom(st), permissionMode: st.permission_mode,
      activity: reconcileRunning(cur.activity, st.running_sessions ?? [], cur.sessionId),
    }));
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
    ...Q.emptyQueue(),
    stash: {},
    activity: {},
    earlier: 0,
    loadingEarlier: false,
    prependTick: 0,

    async init() {
      if (initialized) return;
      initialized = true;
      const b = backend();

      // 所有事件都流经这里。当前会话的事件进 reducer；其他会话（后台运行的）的事件累积到它的 stash，
      // 并更新会话列表用的活动状态。一轮结束后刷新会话列表和状态栏数据。
      b.onEvent((ev) => {
        const sid = ev.session_id;
        const cur = get().sessionId;
        if (!cur || sid === cur) {
          set((s) => applyEvent(s, ev));
        } else {
          set((s) => ({
            stash: s.stash[sid] ? { ...s.stash, [sid]: { ...s.stash[sid], ...applyEvent(s.stash[sid], ev) } } : s.stash,
            activity: withActivity(s.activity, sid, ev),
          }));
          if (ev.type === "permission_request") {
            const name = get().sessions.find((x) => x.id === sid)?.title || t("另一个会话");
            set({ toast: t("会话「{name}」在等你确认权限，点会话列表里的它即可处理", { name }) });
          }
        }
        // 发送队列跟着真实事件走：后端开始新一轮 = 那条消息送达；后端拒绝 = 标记失败；一轮结束 = 轮到下一条。
        // 队列里的消息都绑定了会话，所以后台会话的事件也要处理。
        if (ev.type === "turn_start" && !ev.data.replay) {
          set((s) => Q.onTurnStarted(s, sid));
          void refreshMeta(); // 新会话的第一轮开始后就出现在会话列表里（后端把运行中的会话也列出来）
        }
        else if (ev.type === "error" && ["send_failed", "busy", "attachment_error"].includes(ev.data.code)) set((s) => Q.onSendRejected(s, sid, ev.data.message));
        else if (ev.type === "turn_end" && !ev.data.replay) {
          set((s) => {
            const q = Q.onTurnEnded(s, sid, ev.data.reason);
            // 后台会话跑完、队列里也没有后续消息：丢掉它的 stash，再打开时从后端重新加载（历史与翻页都是完整的）。
            const done = sid !== s.sessionId && !!s.stash[sid] && !s.queue.some((m) => m.sessionId === sid);
            return done ? { ...q, stash: omit(s.stash, sid) } : q;
          });
          void drain(sid);
        }
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
          activity: reconcileRunning({}, st.running_sessions ?? [], st.session_id),
        });
      });
    },

    send: (text, attachments = []) =>
      guard(async () => {
        const sid = get().sessionId;
        if (!sid) throw new Error(t("还没有可用的会话"));
        // 统一走队列：空闲时马上发出，运行中先排队；这样失败重试、顺序、按会话隔离只有一套逻辑。
        set((s) => Q.enqueue(s, { id: `q_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`, sessionId: sid, text, attachments }));
        await drain();
      }),
    retryQueued: (id) =>
      guard(async () => {
        set((s) => Q.retry(s, id));
        await drain();
      }),
    removeQueued: (id) => set((s) => Q.remove(s, id)),
    moveQueuedFirst: (id) => set((s) => Q.moveFirst(s, id)),
    resumeQueue: () =>
      guard(async () => {
        const sid = get().sessionId;
        if (sid) set((s) => Q.resume(s, sid));
        await drain();
      }),
    cancel: () => guard(() => backend().cancel(get().sessionId ?? undefined)),
    respondPermission: (id, decision) => guard(() => backend().respondPermission(id, decision)),

    newSession: () =>
      guard(async () => {
        const st = await backend().newSession();
        leaveCurrent();
        set({ ...initialState(st.session_id), provider: st.provider, model: st.model, title: "", earlier: 0, loadingEarlier: false, ...wsFrom(st) });
        enter(st.session_id);
        void get().refreshWorkspace();
      }),

    loadSession: (id) =>
      guard(async () => {
        if (id === get().sessionId) return;
        // 先切换到目标会话再调用后端：这样加载期间旧会话残留的实时事件会被归到它自己的 stash，不会混进新会话的界面。
        const prev = get();
        const stashed = prev.stash[id];
        leaveCurrent();
        set({ ...initialState(id), provider: prev.provider, model: prev.model, earlier: 0, loadingEarlier: false });
        try {
          const r = await backend().loadSession(id);
          const st = r.status;
          if (stashed) {
            // 这个会话在后台跑过：用后台累积的完整界面状态（包含正在输出的内容），不用后端的重放（那里没有流式到一半的消息）。
            // 后台期间历史可能超出后端的分页窗口，翻页的游标对不上，所以不再提供"加载更早"。
            const latest = get().stash[id] ?? stashed;
            const { earlier: _e, title: _t, paged: _p, ...view } = latest;
            void _e; void _t; void _p;
            set({ ...view, running: st.running, title: st.title, provider: st.provider, model: st.model, sessionUsage: st.usage, earlier: 0, ...wsFrom(st) });
          } else {
            // 历史作为返回值一次性给出（只有最近几轮）：折叠完再 set 一次，界面只渲染一次。
            set((s) => ({
              ...foldEvents(s, r.events),
              running: st.running || s.running,
              title: st.title, provider: st.provider, model: st.model, sessionUsage: st.usage, earlier: r.earlier,
              ...wsFrom(st),
            }));
          }
          enter(id);
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
            running: prev.running,
          });
          if (prev.sessionId) enter(prev.sessionId);
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
        set((s) => ({ queue: s.queue.filter((m) => m.sessionId !== id), stash: omit(s.stash, id), activity: omit(s.activity, id) })); // 会话没了，它的待发送消息也没有去处
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
        // 清空历史之后所有旧会话都没了，待发送的消息一并丢弃
        set({ ...initialState(st.session_id), provider: st.provider, model: st.model, title: "", sessionUsage: st.usage, earlier: 0, loadingEarlier: false, ...wsFrom(st), ...Q.emptyQueue(), stash: {}, activity: {} });
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
