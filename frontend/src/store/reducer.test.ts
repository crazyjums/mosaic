import { describe, expect, it } from "vitest";
import type { AgentEvent } from "../protocol/events";
import raw from "../bridge/demo-events.jsonl?raw";
import { applyEvent, initialState, type AssistantItem } from "./reducer";

// 用真实 CLI 录制的事件流做测试数据：保证前端 reducer 与 Go 端协议一致。
const recorded: AgentEvent[] = raw
  .split("\n")
  .filter((l) => l.trim())
  .map((l) => JSON.parse(l));

const replay = (events: AgentEvent[]) => events.reduce(applyEvent, initialState());

describe("applyEvent", () => {
  it("把录制的 demo 事件流还原成时间线", () => {
    const s = replay(recorded);
    expect(s.running).toBe(false);
    expect(s.permissions).toHaveLength(0);

    const kinds = s.timeline.map((t) => t.kind);
    expect(kinds).toEqual(["user", "assistant", "assistant", "assistant", "assistant", "turn_end"]);

    const first = s.timeline[1] as AssistantItem;
    expect(first.streaming).toBe(false);
    // 多个 thinking_delta / text_delta 被合并成一个块
    expect(first.blocks.map((b) => b.type)).toEqual(["thinking", "text", "tool"]);

    const last = s.timeline[4] as AssistantItem;
    expect(last.blocks.map((b) => b.type)).toEqual(["thinking", "text", "image"]);

    const tools = Object.values(s.tools);
    expect(tools.map((t) => t.name)).toEqual(["list_dir", "write_file", "shell"]);
    expect(tools.every((t) => t.status === "done")).toBe(true);
    expect(tools[1].permission?.decision).toBe("allow");
    expect(s.sessionUsage.output_tokens).toBeGreaterThan(0);
  });

  it("运行中：权限请求进入队列，工具进入等待确认状态", () => {
    const idx = recorded.findIndex((e) => e.type === "permission_request");
    const s = replay(recorded.slice(0, idx + 1));
    expect(s.running).toBe(true);
    expect(s.permissions).toHaveLength(1);
    const req = s.permissions[0];
    expect(s.tools[req.call_id].status).toBe("awaiting_permission");

    const denied = applyEvent(s, {
      ...recorded[idx],
      type: "permission_resolved",
      data: { request_id: req.request_id, call_id: req.call_id, decision: "deny" },
    } as AgentEvent);
    expect(denied.permissions).toHaveLength(0);
    expect(denied.tools[req.call_id].status).toBe("denied");
  });

  it("运行中每次 message_end 都实时累加会话用量，turn_end 用权威值覆盖", () => {
    const firstEnd = recorded.findIndex((e) => e.type === "message_end");
    const mid = replay(recorded.slice(0, firstEnd + 1));
    expect(mid.sessionUsage.output_tokens).toBeGreaterThan(0);
    const done = replay(recorded);
    const end = recorded[recorded.length - 1];
    expect(end.type).toBe("turn_end");
    if (end.type === "turn_end") expect(done.sessionUsage).toEqual(end.data.session_usage);
  });

  it("忽略其他会话的事件", () => {
    const s = initialState("sess_other");
    expect(applyEvent(s, recorded[0])).toBe(s);
  });

  it("没有 thinking_delta 时不产生思考块（降级）", () => {
    const s = replay(recorded.filter((e) => e.type !== "thinking_delta"));
    for (const it of s.timeline) {
      if (it.kind === "assistant") expect(it.blocks.some((b) => b.type === "thinking")).toBe(false);
    }
  });

  it("协议版本不一致时给出一次提示", () => {
    const evs = recorded.slice(0, 3).map((e) => ({ ...e, v: 99 }) as AgentEvent);
    const s = replay(evs);
    expect(s.timeline.filter((t) => t.kind === "notice")).toHaveLength(1);
  });
});
