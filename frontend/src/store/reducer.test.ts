import { describe, expect, it } from "vitest";
import type { AgentEvent } from "../protocol/events";
import raw from "../bridge/demo-events.jsonl?raw";
import { applyEvent, hookLine, initialState, type AssistantItem } from "./reducer";

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

  it("route 事件：消息头改为实际应答的成员，并记录被转交的尝试", () => {
    const base = { v: 1, id: "e", seq: 1, session_id: "s", agent_id: "main", ts: "2026-10-25T00:00:00Z" };
    const events = [
      { ...base, type: "message_start", message_id: "m1", data: { provider: "主力", model: "auto", step: 1 } },
      {
        ...base, type: "route", message_id: "m1",
        data: { group: "主力", profile: "deepseek", model: "v4", try: 2, handed_on: [{ profile: "anthropic", model: "sonnet", kind: "rate_limit", message: "429" }] },
      },
    ] as AgentEvent[];
    const m = replay(events).timeline[0] as AssistantItem;
    expect([m.provider, m.model]).toEqual(["deepseek", "v4"]);
    expect(m.route).toEqual({ group: "主力", tried: 2, handedOn: [{ profile: "anthropic", model: "sonnet", kind: "rate_limit", message: "429" }] });
  });
});

describe("子 agent", () => {
  const mk = (type: string, data: unknown, agent = "main", parent?: string, message_id?: string) =>
    ({ v: 1, id: `e_${Math.random()}`, seq: 0, ts: "2026-01-01T00:00:00Z", session_id: "s", agent_id: agent, parent_id: parent, message_id, type, data }) as unknown as AgentEvent;

  it("subagent_start 记下派出它的 task 调用；子 agent 的消息带自己的 agent_id；结束时记下状态和结论", () => {
    let s = initialState("s");
    s = applyEvent(s, mk("subagent_start", { agent_id: "a1", parent_id: "main", name: "explorer", task: "看看", call_id: "c1" }));
    expect(s.agents.a1).toMatchObject({ name: "explorer", callId: "c1", status: "running" });
    s = applyEvent(s, mk("message_start", { provider: "fake", model: "demo", step: 1 }, "a1", "main", "m1"));
    const item = s.timeline.find((it) => it.kind === "assistant") as AssistantItem;
    expect(item.agentId).toBe("a1");
    s = applyEvent(s, mk("subagent_end", { agent_id: "a1", parent_id: "main", status: "completed", summary: "有 a.txt" }));
    expect(s.agents.a1).toMatchObject({ status: "completed", summary: "有 a.txt" });
  });
});

describe("钩子", () => {
  const mk = (type: string, data: unknown, message_id?: string) =>
    ({ v: 1, id: `e_${Math.random()}`, seq: 0, ts: "2026-01-01T00:00:00Z", session_id: "s", agent_id: "main", message_id, type, data }) as unknown as AgentEvent;

  it("带 call_id 的挂在工具卡片上；不带的在对话里留一行", () => {
    let s = initialState("s");
    s = applyEvent(s, mk("message_start", { provider: "fake", model: "demo", step: 1 }, "m1"));
    s = applyEvent(s, mk("tool_call", { call_id: "c1", name: "shell", input: { command: "rm -rf /" }, risk: "exec" }, "m1"));
    s = applyEvent(s, mk("hook_run", { hook: "PreToolUse", source: "plugin:guard", source_name: "守卫", command: "./guard.sh", call_id: "c1", outcome: "blocked", message: "不许删根目录", duration_ms: 12 }));
    expect(s.tools.c1.hooks).toHaveLength(1);
    expect(hookLine(s.tools.c1.hooks![0])).toBe("钩子 · 守卫 · PreToolUse · 已阻止：不许删根目录");

    s = applyEvent(s, mk("hook_run", { hook: "UserPromptSubmit", source: "plugin:ctx", command: "date", outcome: "ok", added_context: true, duration_ms: 3 }));
    const last = s.timeline[s.timeline.length - 1];
    expect(last).toMatchObject({ kind: "notice", text: "钩子 · plugin:ctx · UserPromptSubmit · 完成（补充了上下文）" });
  });
});
