import { beforeEach, describe, expect, it, vi } from "vitest";

// 多个会话同时运行时前端的行为：切走的会话转入后台继续累积事件，切回来原样恢复；
// 后台的权限请求、结束都会在会话列表里留下标记；切换不再要求先停止。
type Handler = (ev: unknown) => void;
let handler: Handler = () => {};
let currentSession = "A";
let running: string[] = [];

const status = (sid: string) => ({ session_id: sid, provider: "p", model: "m", title: `title-${sid}`, usage: { input_tokens: 0, output_tokens: 0 }, running: running.includes(sid), running_sessions: running, permission_mode: "default", workspace: "/w" });
const fake = {
  kind: "demo" as const,
  onEvent: vi.fn((cb: Handler) => {
    handler = cb;
    return () => {};
  }),
  getStatus: vi.fn(async () => status(currentSession)),
  getProfiles: vi.fn(async () => []),
  listSessions: vi.fn(async () => [{ id: "A", title: "会话A" }, { id: "B", title: "会话B" }]),
  getWorkspaceInfo: vi.fn(async () => null),
  send: vi.fn(async () => {}),
  cancel: vi.fn(async () => {}),
  newSession: vi.fn(async () => {
    currentSession = "B";
    return status("B");
  }),
  loadSession: vi.fn(async (id: string) => {
    currentSession = id;
    return { status: status(id), events: [], earlier: 0 };
  }),
  respondPermission: vi.fn(async () => {}),
};
vi.mock("../bridge", () => ({ backend: () => fake }));
const confirmSpy = vi.fn(() => true);
vi.stubGlobal("confirm", confirmSpy);

const { useChat } = await import("./chatStore");
await useChat.getState().init();

let seq = 0;
const ev = (type: string, data: Record<string, unknown>, session: string) =>
  handler({ v: 1, id: `e${++seq}`, seq, ts: "2026-01-01T00:00:00Z", session_id: session, agent_id: "main", type, data });
const turnStart = (s: string) => ev("turn_start", { turn_id: "t", provider: "p", model: "m" }, s);
const turnEnd = (s: string) => ev("turn_end", { turn_id: "t", reason: "completed", steps: 1, usage: { input_tokens: 0, output_tokens: 0 }, session_usage: { input_tokens: 0, output_tokens: 0 } }, s);
const say = (s: string, text: string, start = false) => {
  if (start) handler({ v: 1, id: `e${++seq}`, seq, ts: "2026-01-01T00:00:00Z", session_id: s, agent_id: "main", message_id: "m1", type: "message_start", data: { provider: "p", model: "m", step: 1 } });
  handler({ v: 1, id: `e${++seq}`, seq, ts: "2026-01-01T00:00:00Z", session_id: s, agent_id: "main", message_id: "m1", type: "text_delta", data: { text } });
};
const assistantText = () => useChat.getState().timeline.flatMap((t) => (t.kind === "assistant" ? t.blocks.flatMap((b) => (b.type === "text" ? [b.text] : [])) : []));

describe("多会话并发（前端）", () => {
  beforeEach(() => {
    currentSession = "A";
    running = [];
    confirmSpy.mockClear();
    useChat.setState({ sessionId: "A", running: false, timeline: [], tools: {}, permissions: [], queue: [], paused: {}, stash: {}, activity: {}, toast: null, earlier: 0, prependTick: 0, title: "" });
  });

  it("运行中新建会话：不再弹确认也不会停止，原会话转入后台继续累积事件，切回来原样恢复", async () => {
    turnStart("A");
    say("A", "第一段", true);
    running = ["A"];
    expect(useChat.getState().running).toBe(true);

    await useChat.getState().newSession();
    expect(confirmSpy).not.toHaveBeenCalled();
    expect(fake.cancel).not.toHaveBeenCalled();
    let s = useChat.getState();
    expect(s.sessionId).toBe("B");
    expect(s.running).toBe(false); // B 没有在运行
    expect(s.timeline).toEqual([]);
    expect(s.stash.A).toBeTruthy();
    expect(s.activity.A.running).toBe(true);

    // A 在后台继续输出：事件进 A 的 stash，不会出现在当前（B）的界面里
    say("A", "第二段");
    expect(useChat.getState().timeline).toEqual([]);

    // 回到 A：看到后台累积的内容，仍在运行
    await useChat.getState().loadSession("A");
    s = useChat.getState();
    expect(s.sessionId).toBe("A");
    expect(s.running).toBe(true);
    expect(assistantText().join("")).toBe("第一段第二段");
    expect(s.stash.A).toBeUndefined();
    expect(s.activity.A).toBeUndefined();
  });

  it("没有在运行、也没有待发送消息的会话，离开时不占后台状态", async () => {
    await useChat.getState().newSession();
    expect(useChat.getState().stash.A).toBeUndefined();
  });

  it("后台会话的权限请求：会话列表标记待确认并提示；回到该会话后弹窗在那里", async () => {
    turnStart("A");
    running = ["A"];
    await useChat.getState().newSession();
    ev("permission_request", { request_id: "p1", call_id: "c1", tool: "write_file", risk: "write", summary: "写入 a.txt", input: {} }, "A");
    let s = useChat.getState();
    expect(s.activity.A.waiting).toBe(1);
    expect(s.permissions).toEqual([]); // 当前是 B，弹窗不会跳出来盖住 B
    expect(s.toast).toContain("会话A");

    await useChat.getState().loadSession("A");
    s = useChat.getState();
    expect(s.permissions.map((p) => p.request_id)).toEqual(["p1"]);
    await useChat.getState().respondPermission("p1", "allow");
    expect(fake.respondPermission).toHaveBeenCalledWith("p1", "allow");
  });

  it("后台会话跑完：标记有新结果，没有后续消息时丢掉后台状态；打开后清除标记", async () => {
    turnStart("A");
    running = ["A"];
    await useChat.getState().newSession();
    running = [];
    turnEnd("A");
    let s = useChat.getState();
    expect(s.activity.A).toEqual({ running: false, waiting: 0, unread: true });
    expect(s.stash.A).toBeUndefined();

    await useChat.getState().loadSession("A");
    expect(useChat.getState().activity.A).toBeUndefined();
    expect(fake.loadSession).toHaveBeenCalledWith("A");
  });

  it("后台会话有待发送消息：离开时保留后台状态，它跑完一轮后接着发给它自己", async () => {
    turnStart("A");
    running = ["A"];
    await useChat.getState().send("排队的一条"); // A 在运行 → 进队列
    await useChat.getState().newSession();
    expect(useChat.getState().stash.A).toBeTruthy(); // 有待发送消息，保留
    fake.send.mockClear();
    turnEnd("A");
    await new Promise((r) => setTimeout(r, 0));
    expect(fake.send).toHaveBeenCalledWith("A", "排队的一条", undefined);
  });

  it("取消只针对当前会话", async () => {
    await useChat.getState().cancel();
    expect(fake.cancel).toHaveBeenCalledWith("A");
  });

  it("删除会话时清掉它的后台状态和待发送消息", async () => {
    turnStart("A");
    running = ["A"];
    await useChat.getState().send("x");
    await useChat.getState().newSession();
    (fake as unknown as { deleteSession: () => Promise<void> }).deleteSession = vi.fn(async () => {});
    await useChat.getState().deleteSession("A");
    const s = useChat.getState();
    expect(s.stash.A).toBeUndefined();
    expect(s.activity.A).toBeUndefined();
    expect(s.queue.some((m) => m.sessionId === "A")).toBe(false);
  });
});
