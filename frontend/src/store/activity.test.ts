import { describe, expect, it } from "vitest";
import type { AgentEvent } from "../protocol/events";
import { applyActivity, reconcileRunning } from "./activity";

const ev = (type: string, data: unknown = {}): AgentEvent => ({ v: 1, id: "e", seq: 1, session_id: "s", agent_id: "main", ts: "", type, data }) as unknown as AgentEvent;

describe("applyActivity", () => {
  it("一轮从开始到结束：运行中 → 有未读结果", () => {
    let a = applyActivity(undefined, ev("turn_start", {}));
    expect(a).toEqual({ running: true, waiting: 0, unread: false });
    a = applyActivity(a, ev("permission_request", {}));
    a = applyActivity(a, ev("permission_request", {}));
    expect(a?.waiting).toBe(2);
    a = applyActivity(a, ev("permission_resolved", {}));
    expect(a?.waiting).toBe(1);
    a = applyActivity(a, ev("turn_end", { reason: "completed" }));
    expect(a).toEqual({ running: false, waiting: 0, unread: true });
  });
  it("重放的事件不改变状态；待确认数不会小于 0", () => {
    expect(applyActivity(undefined, ev("turn_start", { replay: true }))).toBeUndefined();
    expect(applyActivity(undefined, ev("turn_end", { replay: true }))).toBeUndefined();
    expect(applyActivity({ running: true, waiting: 0, unread: false }, ev("permission_resolved"))?.waiting).toBe(0);
  });
  it("其他事件原样返回", () => {
    const a = { running: true, waiting: 1, unread: false };
    expect(applyActivity(a, ev("text_delta", { text: "x" }))).toBe(a);
  });
});

describe("reconcileRunning", () => {
  it("补上后端在运行但本地不知道的会话；不清除已有记录（快照可能已过时）；当前会话不记录", () => {
    const out = reconcileRunning({ a: { running: true, waiting: 1, unread: false }, b: { running: false, waiting: 0, unread: true } }, ["c", "cur"], "cur");
    expect(out.a).toEqual({ running: true, waiting: 1, unread: false });
    expect(out.b.unread).toBe(true);
    expect(out.c).toEqual({ running: true, waiting: 0, unread: false });
    expect(out.cur).toBeUndefined();
  });
});
