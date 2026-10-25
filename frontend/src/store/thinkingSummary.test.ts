import { describe, expect, it } from "vitest";
import type { AssistantItem } from "./reducer";
import { thinkingSummary } from "./thinkingSummary";

const item = (blocks: AssistantItem["blocks"]): AssistantItem => ({ kind: "assistant", id: "m", blocks } as unknown as AssistantItem);

describe("thinkingSummary", () => {
  it("没有思考文字时返回 null（不编造）", () => {
    expect(thinkingSummary([item([{ type: "text", text: "hi" }])])).toBeNull();
    expect(thinkingSummary([item([{ type: "thinking", text: "  \n ", redacted: false }])])).toBeNull();
  });
  it("加密/脱敏的思考不展示", () => {
    expect(thinkingSummary([item([{ type: "thinking", text: "secret", redacted: true }])])).toBeNull();
  });
  it("取最近一段，压成单行", () => {
    const s = thinkingSummary([item([{ type: "thinking", text: "第一段\n\n先看一下\n  配置文件   再决定", redacted: false }])]);
    expect(s).toBe("先看一下 配置文件 再决定");
  });
  it("优先最后一步的思考，回退到更早的步骤", () => {
    const a = item([{ type: "thinking", text: "早先的想法", redacted: false }]);
    const b = item([{ type: "tool", callId: "c" } as never]);
    expect(thinkingSummary([a, b])).toBe("早先的想法");
    const c = item([{ type: "thinking", text: "最新的想法", redacted: false }]);
    expect(thinkingSummary([a, b, c])).toBe("最新的想法");
  });
  it("过长时截断（按字符，不切坏 emoji）", () => {
    const s = thinkingSummary([item([{ type: "thinking", text: "😀".repeat(300), redacted: false }])])!;
    expect(Array.from(s)).toHaveLength(161);
    expect(s.endsWith("…")).toBe(true);
  });
});
