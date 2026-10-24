import { beforeEach, describe, expect, it, vi } from "vitest";

// 用一个最小的假后端替换桥接层：只关心 loadSession / deleteSession 的失败与切换路径。
const fake = {
  kind: "demo" as const,
  loadSession: vi.fn(),
  deleteSession: vi.fn(async () => {}),
  getStatus: vi.fn(async () => ({ session_id: "sess_new", provider: "fake", model: "demo", title: "", usage: { input_tokens: 0, output_tokens: 0 } })),
  listSessions: vi.fn(async () => []),
};
vi.mock("../bridge", () => ({ backend: () => fake }));

const { useChat } = await import("./chatStore");

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
});
