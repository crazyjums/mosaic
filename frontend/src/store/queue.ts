// 发送队列：agent 运行时用户可以继续输入，新消息先排队，当前这一轮结束后按先进先出依次发送。
//
// 为什么在界面层做、而不是让内核排队：内核同一时间只跑一轮（Engine.Send 运行中返回 ErrBusy），
// 这是有意的简化；排队只是"把用户的话先存着"，不改变内核的行为，也不抢占正在运行的请求。
//
// 这里全是纯函数（输入旧状态、输出新状态），状态机的每个分支都能单测；chatStore 只负责把它们接到事件和后端调用上。
//
// 规则：
//   - 每条消息绑定提交时的会话：只会发给那个会话，切换会话不会串发；
//   - FIFO；队首失败时挡在最前面，必须重试或删除，不会被跳过（否则消息顺序会乱）；
//   - 同一时间只有一条在"发送中"（已交给后端、等它开始新一轮），防止重复事件重复发送；
//   - "停止"或一轮以错误结束后，这个会话的队列暂停（消息保留）；用户点"继续发送"或再发一条新消息才会恢复。

import type { Attachment } from "../bridge";

export type QueueStatus = "queued" | "sending" | "failed";

export interface QueuedMessage {
  id: string;
  sessionId: string;
  text: string;
  attachments: Attachment[];
  status: QueueStatus;
  error?: string;
}

export interface QueueState {
  queue: QueuedMessage[];
  /** 按会话暂停：停止 / 出错之后不自动发送剩下的消息 */
  paused: Record<string, boolean>;
}

export const emptyQueue = (): QueueState => ({ queue: [], paused: {} });

const patch = (s: QueueState, id: string, fn: (m: QueuedMessage) => QueuedMessage): QueueState => ({ ...s, queue: s.queue.map((m) => (m.id === id ? fn(m) : m)) });

/** 入队。用户又发了一条新消息，说明想继续：恢复这个会话的队列。 */
export function enqueue(s: QueueState, m: Omit<QueuedMessage, "status" | "error">): QueueState {
  return { queue: [...s.queue, { ...m, status: "queued" }], paused: { ...s.paused, [m.sessionId]: false } };
}

/** 下一条该发送的消息；现在不该发送时返回 null。 */
export function nextToSend(s: QueueState, sessionId: string, running: boolean): QueuedMessage | null {
  if (running || s.paused[sessionId]) return null;
  if (s.queue.some((m) => m.status === "sending")) return null; // 已经有一条交给后端了，等它开始
  const first = s.queue.find((m) => m.sessionId === sessionId);
  return first && first.status === "queued" ? first : null; // 队首失败：等用户处理
}

export const markSending = (s: QueueState, id: string) => patch(s, id, (m) => ({ ...m, status: "sending", error: undefined }));
export const markFailed = (s: QueueState, id: string, error: string) => patch(s, id, (m) => ({ ...m, status: "failed", error }));
/** 重试：回到待发送，并恢复队列。 */
export const retry = (s: QueueState, id: string): QueueState => {
  const m = s.queue.find((x) => x.id === id);
  if (!m) return s;
  return { ...patch(s, id, (x) => ({ ...x, status: "queued", error: undefined })), paused: { ...s.paused, [m.sessionId]: false } };
};
export const remove = (s: QueueState, id: string): QueueState => ({ ...s, queue: s.queue.filter((m) => m.id !== id) });
export const resume = (s: QueueState, sessionId: string): QueueState => ({ ...s, paused: { ...s.paused, [sessionId]: false } });

/** 调整待发送顺序：把这条移到这个会话队列的最前面。不影响已经在发送的那条，也不中断正在运行的请求。 */
export function moveFirst(s: QueueState, id: string): QueueState {
  const m = s.queue.find((x) => x.id === id);
  if (!m || m.status === "sending") return s;
  const rest = s.queue.filter((x) => x.id !== id);
  const at = rest.findIndex((x) => x.sessionId === m.sessionId && x.status !== "sending");
  if (at < 0) return s;
  return { ...s, queue: [...rest.slice(0, at), m, ...rest.slice(at)] };
}

/** 后端开始了新一轮（turn_start）：正在发送的那条已经送达，从队列里去掉。 */
export function onTurnStarted(s: QueueState, sessionId: string): QueueState {
  const i = s.queue.findIndex((m) => m.sessionId === sessionId && m.status === "sending");
  return i < 0 ? s : { ...s, queue: s.queue.filter((_, j) => j !== i) };
}

/** 一轮结束：被取消或出错时暂停这个会话的队列；正常结束不改变（由调用方接着发送下一条）。 */
export function onTurnEnded(s: QueueState, sessionId: string, reason: string): QueueState {
  return reason === "cancelled" || reason === "error" ? { ...s, paused: { ...s.paused, [sessionId]: true } } : s;
}

/** 后端拒绝了这次发送（没有可用厂商、附件不合格、正忙……）：正在发送的那条标记为失败，保留原文。 */
export function onSendRejected(s: QueueState, sessionId: string, error: string): QueueState {
  const i = s.queue.findIndex((m) => m.sessionId === sessionId && m.status === "sending");
  return i < 0 ? s : markFailed(s, s.queue[i].id, error);
}

export const countFor = (s: QueueState, sessionId: string) => s.queue.filter((m) => m.sessionId === sessionId).length;
export const itemsFor = (s: QueueState, sessionId: string) => s.queue.filter((m) => m.sessionId === sessionId);
export const hasSending = (s: QueueState) => s.queue.some((m) => m.status === "sending");
