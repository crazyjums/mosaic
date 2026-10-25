import { describe, expect, it } from "vitest";
import { countFor, emptyQueue, enqueue, hasSending, itemsFor, markFailed, markSending, moveFirst, nextToSend, onSendRejected, onTurnEnded, onTurnStarted, remove, resume, retry, type QueueState } from "./queue";

const msg = (id: string, sessionId = "A", text = id) => ({ id, sessionId, text, attachments: [] });
const fill = (...ms: ReturnType<typeof msg>[]): QueueState => ms.reduce(enqueue, emptyQueue());
const ids = (s: QueueState, sid = "A") => itemsFor(s, sid).map((m) => m.id);

describe("发送队列", () => {
  it("空闲时第一条立即可发；运行中一律不发", () => {
    const s = fill(msg("1"));
    expect(nextToSend(s, "A", false)?.id).toBe("1");
    expect(nextToSend(s, "A", true)).toBeNull();
  });

  it("先进先出：一条开始后才轮到下一条", () => {
    let s = fill(msg("1"), msg("2"), msg("3"));
    expect(nextToSend(s, "A", false)?.id).toBe("1");
    s = markSending(s, "1");
    // 1 已交给后端、还没开始：不能再发 2（防止重复事件造成重复发送）
    expect(hasSending(s)).toBe(true);
    expect(nextToSend(s, "A", false)).toBeNull();
    expect(nextToSend(s, "A", false)).toBeNull();
    s = onTurnStarted(s, "A"); // turn_start：1 送达
    expect(ids(s)).toEqual(["2", "3"]);
    expect(nextToSend(s, "A", true)).toBeNull(); // 这一轮还在运行
    expect(nextToSend(s, "A", false)?.id).toBe("2"); // 结束后才轮到 2
  });

  it("重复的 turn_start 不会误删后面的消息", () => {
    let s = markSending(fill(msg("1"), msg("2")), "1");
    s = onTurnStarted(s, "A");
    s = onTurnStarted(s, "A"); // 同一事件被重复投递
    expect(ids(s)).toEqual(["2"]);
  });

  it("消息只属于提交时的会话，切换会话不串发", () => {
    const s = fill(msg("a1", "A"), msg("b1", "B"));
    expect(nextToSend(s, "B", false)?.id).toBe("b1");
    expect(nextToSend(s, "A", false)?.id).toBe("a1");
    expect(nextToSend(s, "C", false)).toBeNull();
    // A 的 turn_start 不会动 B 的消息
    const sending = markSending(s, "a1");
    expect(ids(onTurnStarted(sending, "B"), "A")).toEqual(["a1"]);
    expect(ids(onTurnStarted(sending, "A"), "B")).toEqual(["b1"]);
    expect(countFor(s, "A")).toBe(1);
  });

  it("发送失败：保留原文，挡在队首直到重试或删除", () => {
    let s = markSending(fill(msg("1"), msg("2")), "1");
    s = onSendRejected(s, "A", "没有可用的模型厂商");
    expect(itemsFor(s, "A")[0]).toMatchObject({ id: "1", status: "failed", error: "没有可用的模型厂商", text: "1" });
    expect(nextToSend(s, "A", false)).toBeNull(); // 不跳过它去发 2
    s = retry(s, "1");
    expect(nextToSend(s, "A", false)?.id).toBe("1");
    // 删除失败项后，队列继续
    s = remove(markFailed(s, "1", "x"), "1");
    expect(nextToSend(s, "A", false)?.id).toBe("2");
  });

  it("停止或出错：暂停并保留队列；再发新消息或点继续才恢复", () => {
    let s = fill(msg("1"), msg("2"));
    s = onTurnEnded(s, "A", "cancelled");
    expect(ids(s)).toEqual(["1", "2"]); // 不清空
    expect(nextToSend(s, "A", false)).toBeNull();
    expect(nextToSend(resume(s, "A"), "A", false)?.id).toBe("1");
    expect(nextToSend(enqueue(s, msg("3")), "A", false)?.id).toBe("1");
    // 出错同样暂停；正常结束 / 达到最大步数不暂停
    expect(nextToSend(onTurnEnded(fill(msg("1")), "A", "error"), "A", false)).toBeNull();
    expect(nextToSend(onTurnEnded(fill(msg("1")), "A", "completed"), "A", false)?.id).toBe("1");
    expect(nextToSend(onTurnEnded(fill(msg("1")), "A", "max_steps"), "A", false)?.id).toBe("1");
  });

  it("暂停只影响这个会话", () => {
    const s = onTurnEnded(fill(msg("a", "A"), msg("b", "B")), "A", "cancelled");
    expect(nextToSend(s, "A", false)).toBeNull();
    expect(nextToSend(s, "B", false)?.id).toBe("b");
  });

  it("调整顺序：移到最前，但不越过正在发送的那条", () => {
    let s = fill(msg("1"), msg("2"), msg("3"));
    expect(ids(moveFirst(s, "3"))).toEqual(["3", "1", "2"]);
    s = markSending(s, "1");
    expect(ids(moveFirst(s, "3"))).toEqual(["1", "3", "2"]);
    expect(moveFirst(s, "1")).toBe(s); // 正在发送的不能动
    // 只在自己会话的队列里调整
    const mixed = fill(msg("a1", "A"), msg("b1", "B"), msg("a2", "A"));
    expect(ids(moveFirst(mixed, "a2"))).toEqual(["a2", "a1"]);
    expect(ids(moveFirst(mixed, "a2"), "B")).toEqual(["b1"]);
  });

  it("没有正在发送的消息时，turn_start / 拒绝事件什么也不改", () => {
    const s = fill(msg("1"));
    expect(onTurnStarted(s, "A")).toBe(s);
    expect(onSendRejected(s, "A", "x")).toBe(s);
  });
});
