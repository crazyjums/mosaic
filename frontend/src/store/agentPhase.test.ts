import { describe, expect, it } from "vitest";
import { livePhase } from "./agentPhase";
import type { AssistantItem } from "./reducer";

const msg = (blocks: AssistantItem["blocks"]): AssistantItem => ({ kind: "assistant", id: "m", agentId: "root", step: 1, provider: "p", model: "m", blocks, streaming: true });
const base = { running: false, permissions: [], timeline: [] as AssistantItem[] };

describe("Agent 阶段", () => {
  it("空闲是静止", () => expect(livePhase(base)).toBe("static"));
  it("运行中还没有输出：开始", () => {
    expect(livePhase({ ...base, running: true })).toBe("start");
    expect(livePhase({ ...base, running: true, timeline: [msg([])] })).toBe("start");
  });
  it("运行中已有输出：思考 / 执行", () => {
    expect(livePhase({ ...base, running: true, timeline: [msg([{ type: "thinking", text: "x", redacted: false }])] })).toBe("thinking");
  });
  it("等待权限确认：需要处理（即使正在运行）", () => {
    expect(livePhase({ ...base, running: true, permissions: [{ request_id: "r" } as never] })).toBe("problem");
  });
});
