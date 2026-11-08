import { describe, expect, it, vi } from "vitest";

// 其他 chatStore 测试的做法：用最小的假后端替换桥接层（chatStore 在加载时会碰 window / backend）。
vi.mock("../bridge", () => ({ backend: () => ({ kind: "demo" as const }) }));
const { useChat } = await import("./chatStore");

// 插件详情里的“在输入框里使用”：把文字交给输入框，不自动发送；同样的文字再点一次也要能再次触发。
describe("输入框草稿", () => {
  it("draftToComposer 设置草稿，每次递增 seq", () => {
    useChat.setState({ composerDraft: null });
    useChat.getState().draftToComposer("/hello ");
    const a = useChat.getState().composerDraft!;
    expect(a.text).toBe("/hello ");
    useChat.setState({ composerDraft: null }); // Composer 取走之后清空
    useChat.getState().draftToComposer("/hello ");
    const b = useChat.getState().composerDraft!;
    expect(b.text).toBe("/hello ");
    expect(b.seq).toBeGreaterThan(0);
    // 没被取走时再次设置：seq 变了，Composer 的 effect 才会再触发
    useChat.getState().draftToComposer("/hello ");
    expect(useChat.getState().composerDraft!.seq).toBe(b.seq + 1);
  });
});
