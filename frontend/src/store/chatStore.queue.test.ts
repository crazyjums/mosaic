import { beforeEach, describe, expect, it, vi } from "vitest";

// 发送队列接到真实事件上的集成测试：用一个假后端，手动"发出"后端事件，验证 FIFO、按会话隔离、失败重试、停止后暂停。
type Handler = (ev: unknown) => void;
let handler: Handler = () => {};
const sent: { sessionId: string; text: string; attachments?: unknown[] }[] = [];
let failNext: string | null = null;

const status = (sid: string) => ({ session_id: sid, provider: "p", model: "m", title: "", usage: { input_tokens: 0, output_tokens: 0 }, running: false, permission_mode: "default" });
let currentSession = "A";
const fake = {
  kind: "demo" as const,
  onEvent: vi.fn((cb: Handler) => {
    handler = cb;
    return () => {};
  }),
  getStatus: vi.fn(async () => status(currentSession)),
  getProfiles: vi.fn(async () => []),
  listSessions: vi.fn(async () => []),
  getWorkspaceInfo: vi.fn(async () => null),
  send: vi.fn(async (sessionId: string, text: string, attachments?: unknown[]) => {
    if (failNext) {
      const e = failNext;
      failNext = null;
      throw new Error(e);
    }
    sent.push({ sessionId, text, attachments });
  }),
  cancel: vi.fn(async () => {}),
};
vi.mock("../bridge", () => ({ backend: () => fake }));

const { useChat } = await import("./chatStore");
await useChat.getState().init();

let seq = 0;
const ev = (type: string, data: Record<string, unknown> = {}, session = currentSession) => handler({ v: 1, id: `e${++seq}`, seq, ts: "2026-01-01T00:00:00Z", session_id: session, agent_id: "main", type, data });
const turnStart = (s?: string) => ev("turn_start", { turn_id: "t", provider: "p", model: "m" }, s);
const turnEnd = (reason = "completed", s?: string) => ev("turn_end", { turn_id: "t", reason, steps: 1, usage: { input_tokens: 0, output_tokens: 0 }, session_usage: { input_tokens: 0, output_tokens: 0 } }, s);
const texts = () => useChat.getState().queue.map((m) => m.text);
const flush = () => new Promise((r) => setTimeout(r, 0));

describe("发送队列（接到事件上）", () => {
  beforeEach(() => {
    sent.length = 0;
    failNext = null;
    currentSession = "A";
    useChat.setState({ sessionId: "A", running: false, queue: [], paused: {}, permissions: [] });
  });

  it("空闲时直接发送；运行中排队，一轮结束后按 FIFO 逐条发送", async () => {
    await useChat.getState().send("one");
    expect(sent.map((s) => s.text)).toEqual(["one"]);
    turnStart(); // 后端开始：one 送达，从队列去掉
    expect(texts()).toEqual([]);
    expect(useChat.getState().running).toBe(true);

    await useChat.getState().send("two");
    await useChat.getState().send("three");
    expect(sent.map((s) => s.text)).toEqual(["one"]); // 运行中不发
    expect(texts()).toEqual(["two", "three"]);

    turnEnd();
    await flush();
    expect(sent.map((s) => s.text)).toEqual(["one", "two"]); // 只发一条
    turnEnd(); // 重复的 turn_end 不会让同一队列项重复发送，也不会提前发 three
    await flush();
    expect(sent.map((s) => s.text)).toEqual(["one", "two"]);

    turnStart();
    turnEnd();
    await flush();
    expect(sent.map((s) => s.text)).toEqual(["one", "two", "three"]);
  });

  it("消息带着附件一起发送", async () => {
    await useChat.getState().send("看看", [{ name: "a.md", text: "x" }]);
    expect(sent[0]).toEqual({ sessionId: "A", text: "看看", attachments: [{ name: "a.md", text: "x" }] });
  });

  it("后端拒绝（如没有可用厂商）：消息保留、标记失败，重试后发出", async () => {
    await useChat.getState().send("hello");
    ev("error", { code: "send_failed", message: "还没有可用的模型厂商", fatal: true });
    const m = useChat.getState().queue[0];
    expect(m).toMatchObject({ text: "hello", status: "failed", error: "还没有可用的模型厂商" });
    await useChat.getState().send("next"); // 失败的挡在前面，后面的不会越过它
    expect(sent.map((s) => s.text)).toEqual(["hello"]);
    await useChat.getState().retryQueued(m.id);
    expect(sent.map((s) => s.text)).toEqual(["hello", "hello"]);
  });

  it("调用本身抛错也保留消息", async () => {
    failNext = "ErrBusy";
    await useChat.getState().send("x");
    expect(useChat.getState().queue[0]).toMatchObject({ text: "x", status: "failed", error: "ErrBusy" });
  });

  it("停止当前轮：队列保留并暂停，不自动发送；继续发送后才发", async () => {
    await useChat.getState().send("one");
    turnStart();
    await useChat.getState().send("two");
    turnEnd("cancelled");
    await flush();
    expect(sent.map((s) => s.text)).toEqual(["one"]);
    expect(texts()).toEqual(["two"]);
    expect(useChat.getState().paused["A"]).toBe(true);
    await useChat.getState().resumeQueue();
    expect(sent.map((s) => s.text)).toEqual(["one", "two"]);
  });

  it("消息属于提交时的会话：切到别的会话后，后台会话跑完一轮会接着发它自己的队列，不会发给当前会话", async () => {
    await useChat.getState().send("one");
    turnStart();
    await useChat.getState().send("for-A");
    // 用户切到会话 B：A 转入后台继续运行（不再需要先停止）
    currentSession = "B";
    useChat.setState({ sessionId: "B", running: false, activity: { A: { running: true, waiting: 0, unread: false } } });
    turnEnd("completed", "A"); // A 在后台跑完一轮：它的队列接着发，而且是发给 A
    await flush();
    expect(sent.map((s) => `${s.sessionId}:${s.text}`)).toEqual(["A:one", "A:for-A"]);
    expect(useChat.getState().queue.map((m) => `${m.sessionId}:${m.text}`)).toEqual(["A:for-A"]); // 已交给后端、等它开始新一轮
    // 在 B 里发消息：只发给 B
    await useChat.getState().send("for-B");
    expect(sent.map((s) => `${s.sessionId}:${s.text}`)).toEqual(["A:one", "A:for-A", "B:for-B"]);
    // A 的新一轮开始后，那条从队列里去掉
    turnStart("A");
    expect(useChat.getState().queue.some((m) => m.text === "for-A")).toBe(false);
  });

  it("删除、置前", async () => {
    await useChat.getState().send("one");
    turnStart();
    await useChat.getState().send("a");
    await useChat.getState().send("b");
    const [a, b] = useChat.getState().queue;
    useChat.getState().moveQueuedFirst(b.id);
    expect(texts()).toEqual(["b", "a"]);
    useChat.getState().removeQueued(a.id);
    expect(texts()).toEqual(["b"]);
  });
});
