import type { AgentEvent } from "../protocol/events";

// 非当前会话的运行情况：会话列表据此显示"运行中 / 待确认 / 有新结果"。
//
// 多个会话可以同时运行，用户只看着其中一个；其他会话的事件仍然会到达。这里只记最少的几个数字，
// 完整的时间线放在 chatStore 的 stash 里（只为"离开时还在运行或有待发送消息"的会话保存）。

export interface SessionActivity {
  running: boolean;
  /** 等待用户确认的权限请求数 */
  waiting: number;
  /** 离开之后有一轮结束了，用户还没看过 */
  unread: boolean;
}

export const idleActivity = (): SessionActivity => ({ running: false, waiting: 0, unread: false });

/** 用一个事件更新某个非当前会话的活动状态。 */
export function applyActivity(a: SessionActivity | undefined, ev: AgentEvent): SessionActivity | undefined {
  const cur = a ?? idleActivity();
  switch (ev.type) {
    case "turn_start":
      return ev.data.replay ? a : { ...cur, running: true };
    case "turn_end":
      return ev.data.replay ? a : { running: false, waiting: 0, unread: true };
    case "permission_request":
      return { ...cur, waiting: cur.waiting + 1 };
    case "permission_resolved":
      return { ...cur, waiting: Math.max(0, cur.waiting - 1) };
    default:
      return a;
  }
}

/**
 * 用后端报告的"正在运行的会话"补全本地记录（界面刚连上、或错过了 turn_start 时）。当前会话不在这里记录。
 *
 * 只补不清：状态请求和事件是两条独立的通道，请求返回时可能已经过时（请求发出后才开始/结束的轮），
 * 拿过时的快照去清除本地状态会让标记闪烁甚至出错。结束由 turn_end 事件负责；
 * 万一漏了，打开那个会话时会按后端的真实状态重新同步。
 */
export function reconcileRunning(act: Record<string, SessionActivity>, running: string[], currentId: string | null): Record<string, SessionActivity> {
  const out = { ...act };
  for (const id of running) if (id !== currentId && !out[id]) out[id] = { running: true, waiting: 0, unread: false };
  return out;
}

/** 会话列表里需要特别标记的会话数（运行中或有未读结果） */
export const attentionCount = (act: Record<string, SessionActivity>) => Object.values(act).filter((a) => a.running || a.waiting > 0 || a.unread).length;
