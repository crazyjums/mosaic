import { beforeEach, describe, expect, it, vi } from "vitest";

// 用一个最小的假后端替换桥接层：只关心 loadSession / deleteSession 的失败与切换路径。
const fake = {
  kind: "demo" as const,
  loadSession: vi.fn(),
  loadEarlierHistory: vi.fn(),
  deleteSession: vi.fn(async () => {}),
  getStatus: vi.fn(async () => ({ session_id: "sess_new", provider: "fake", model: "demo", title: "", usage: { input_tokens: 0, output_tokens: 0 } })),
  listSessions: vi.fn(async () => []),
};
vi.mock("../bridge", () => ({ backend: () => fake }));

const { useChat } = await import("./chatStore");

// 一轮历史重放：turn_start → user_message → turn_end
let seq = 0;
const turn = (sessionId: string, text: string) =>
  [
    { type: "turn_start", data: { turn_id: "t_" + text, provider: "fake", model: "demo", replay: true } },
    { type: "user_message", data: { text } },
    { type: "turn_end", data: { turn_id: "t_" + text, reason: "completed", steps: 1, usage: { input_tokens: 0, output_tokens: 0 }, session_usage: { input_tokens: 0, output_tokens: 0 }, replay: true } },
  ].map((e) => ({ v: 1, id: `ev_${++seq}`, seq: 0, ts: "2026-01-01T00:00:00Z", session_id: sessionId, agent_id: "root", ...e }));

describe("chatStore", () => {
  beforeEach(() => {
    useChat.setState({
      sessionId: "sess_cur",
      timeline: [{ kind: "user", id: "u1", text: "hello" }],
      running: false,
      toast: null,
    });
  });

  it("loadSession 失败时恢复原会话状态", async () => {
    fake.loadSession.mockRejectedValueOnce(new Error("session not found"));
    await useChat.getState().loadSession("sess_missing");
    const s = useChat.getState();
    expect(s.sessionId).toBe("sess_cur");
    expect(s.timeline).toHaveLength(1);
    expect(s.toast).toBe("session not found");
  });

  it("删除当前会话后切换到后端的新会话", async () => {
    await useChat.getState().deleteSession("sess_cur");
    const s = useChat.getState();
    expect(s.sessionId).toBe("sess_new");
    expect(s.timeline).toHaveLength(0);
  });

  it("删除其他会话不影响当前界面", async () => {
    await useChat.getState().deleteSession("sess_other");
    expect(useChat.getState().sessionId).toBe("sess_cur");
    expect(useChat.getState().timeline).toHaveLength(1);
  });

  it("loadSession 一次折叠最近几轮，并记下还有多少轮没加载", async () => {
    fake.loadSession.mockResolvedValueOnce({
      status: { session_id: "sess_long", title: "长会话", provider: "fake", model: "demo", usage: { input_tokens: 0, output_tokens: 0 } },
      events: [...turn("sess_long", "第 8 轮"), ...turn("sess_long", "第 9 轮")],
      earlier: 7,
    });
    await useChat.getState().loadSession("sess_long");
    const s = useChat.getState();
    expect(s.sessionId).toBe("sess_long");
    expect(s.timeline.filter((i) => i.kind === "user")).toHaveLength(2);
    expect(s.earlier).toBe(7);
    expect(s.title).toBe("长会话");
  });

  it("loadEarlier 把更早的一页放到前面并更新剩余轮数", async () => {
    useChat.setState({ sessionId: "sess_long", timeline: [{ kind: "user", id: "u9", text: "第 9 轮" }], earlier: 2, loadingEarlier: false, prependTick: 0 });
    fake.loadEarlierHistory.mockResolvedValueOnce({ events: [...turn("sess_long", "第 7 轮"), ...turn("sess_long", "第 8 轮")], earlier: 0 });
    await useChat.getState().loadEarlier();
    const s = useChat.getState();
    expect(s.timeline.filter((i) => i.kind === "user").map((i) => (i as { text: string }).text)).toEqual(["第 7 轮", "第 8 轮", "第 9 轮"]);
    expect(s.earlier).toBe(0);
    expect(s.loadingEarlier).toBe(false);
    expect(s.prependTick).toBe(1);
  });

  it("loadEarlier 等待期间切换了会话：丢弃这一页", async () => {
    useChat.setState({ sessionId: "sess_long", timeline: [], earlier: 2, loadingEarlier: false, prependTick: 0 });
    fake.loadEarlierHistory.mockImplementationOnce(async () => {
      useChat.setState({ sessionId: "sess_other", timeline: [], earlier: 0 });
      return { events: turn("sess_long", "旧会话"), earlier: 0 };
    });
    await useChat.getState().loadEarlier();
    expect(useChat.getState().timeline).toHaveLength(0);
    expect(useChat.getState().sessionId).toBe("sess_other");
  });
});
