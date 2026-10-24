import { describe, expect, it } from "vitest";
import type { ProfileInfo } from "../bridge";
import { contextTokens, currentStep, fmtDuration, fmtTokens, fmtUSD, sessionCost } from "./metrics";
import { initialState, type AssistantItem, type ChatState } from "./reducer";

const msg = (provider: string, model: string, input: number, output: number, step = 1): AssistantItem => ({
  kind: "assistant", id: `m${Math.random()}`, agentId: "main", step, provider, model, blocks: [], streaming: false,
  usage: { input_tokens: input, output_tokens: output },
});

const profiles: ProfileInfo[] = [
  {
    name: "a", type: "openai", models: ["big", "small"], default_model: "big", has_api_key: true, available: true,
    model_meta: { big: { context_window: 200000, input_price: 3, output_price: 15 }, small: { input_price: 0.1, output_price: 0.5 } },
  },
  { name: "b", type: "openai", models: ["x"], default_model: "x", has_api_key: true, available: true, model_meta: null },
];

const state = (items: AssistantItem[], extra: Partial<ChatState> = {}): ChatState => ({ ...initialState("s"), timeline: items, ...extra });

describe("metrics", () => {
  it("上下文 = 最近一次调用的输入 + 输出", () => {
    expect(contextTokens(state([]))).toBeNull();
    expect(contextTokens(state([msg("a", "big", 1000, 50), msg("a", "big", 5000, 200)]))).toBe(5200);
  });

  it("逐条消息计价，中途换模型也算得对", () => {
    const c = sessionCost(state([msg("a", "big", 1_000_000, 0), msg("a", "small", 1_000_000, 1_000_000)]), profiles);
    expect(c).toEqual({ usd: 3 + 0.1 + 0.5, partial: false });
  });

  it("有未配置单价的模型时标记为部分估算；全都没单价时不显示", () => {
    expect(sessionCost(state([msg("a", "big", 1_000_000, 0), msg("b", "x", 10, 10)]), profiles)?.partial).toBe(true);
    expect(sessionCost(state([msg("b", "x", 10, 10)]), profiles)).toBeNull();
  });

  it("重放的会话没有逐条用量时，用会话累计 × 当前模型单价近似", () => {
    const s = state([], { provider: "a", model: "big", sessionUsage: { input_tokens: 2_000_000, output_tokens: 0 } });
    expect(sessionCost(s, profiles)).toEqual({ usd: 6, partial: true });
  });

  it("运行中显示当前步数", () => {
    expect(currentStep(state([msg("a", "big", 1, 1, 1), msg("a", "big", 1, 1, 3)], { running: true }))).toBe(3);
    expect(currentStep(state([msg("a", "big", 1, 1, 3)]))).toBeNull();
  });

  it("格式化", () => {
    expect(fmtTokens(999)).toBe("999");
    expect(fmtTokens(12345)).toBe("12.3k");
    expect(fmtTokens(1234567)).toBe("1.23M");
    expect(fmtTokens(200000)).toBe("200k");
    expect(fmtTokens(1000000)).toBe("1M");
    expect(fmtTokens(1500000)).toBe("1.5M");
    expect(fmtUSD(0.00432)).toBe("$0.0043");
    expect(fmtUSD(1.234)).toBe("$1.23");
    expect(fmtDuration(8400)).toBe("8s");
    expect(fmtDuration(65000)).toBe("1m05s");
  });
});

import { DEFAULT_CONFIG, ITEM_IDS, normalizeConfig } from "./statusline";

describe("normalizeConfig", () => {
  it("空配置得到默认配置", () => {
    expect(normalizeConfig(null)).toEqual(DEFAULT_CONFIG);
  });
  it("保留顺序和开关，丢弃未知项，补上新增项", () => {
    const c = normalizeConfig({ items: [{ id: "git", enabled: false }, { id: "nope", enabled: true }, { id: "model", enabled: true }, { id: "git", enabled: true }] });
    expect(c.items.slice(0, 2)).toEqual([{ id: "git", enabled: false }, { id: "model", enabled: true }]);
    expect(c.items.map((i) => i.id).sort()).toEqual([...ITEM_IDS].sort());
  });
});
