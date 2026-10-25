// 状态栏用到的派生数据：上下文占用、预估花费、当前步数。
//
// 全部是纯函数（输入状态，输出数字），不放进 reducer：它们依赖厂商配置（单价、上下文窗口），
// 而 reducer 只认事件。写成纯函数也方便单独测试（metrics.test.ts）。

import type { ModelMeta, ProfileInfo } from "../bridge";
import type { Usage } from "../protocol/events";
import type { AssistantItem, ChatState } from "./reducer";

/** 查某个厂商配置档下某个模型的附加信息（上下文窗口、单价）。 */
export function modelMeta(profiles: ProfileInfo[], provider: string, model: string): ModelMeta | undefined {
  return profiles.find((p) => p.name === provider)?.model_meta?.[model];
}

function assistantItems(s: Pick<ChatState, "timeline">): AssistantItem[] {
  return s.timeline.filter((t): t is AssistantItem => t.kind === "assistant");
}

/**
 * 当前上下文大小（token）。
 *
 * 取最近一次模型调用的 输入 + 输出：输入就是厂商实际收到的完整上下文（准确值，不是估算），
 * 而这次的输出下一次也会成为上下文的一部分。还没有调用过模型（或会话是重放的、没有用量）时返回 null。
 */
export function contextTokens(s: Pick<ChatState, "timeline">): number | null {
  const items = assistantItems(s);
  for (let i = items.length - 1; i >= 0; i--) {
    const u = items[i].usage;
    if (u && u.input_tokens > 0) return u.input_tokens + u.output_tokens;
  }
  return null;
}

/** 一次调用的花费（美元）。单价的单位是"美元 / 百万 token"。缓存读取按输入单价计（偏保守的上限估计）。 */
function costOf(u: Usage, meta: ModelMeta | undefined): number | null {
  if (!meta || (!meta.input_price && !meta.output_price)) return null;
  return (u.input_tokens * (meta.input_price ?? 0) + u.output_tokens * (meta.output_price ?? 0)) / 1e6;
}

export interface CostEstimate {
  usd: number;
  /** true 表示有调用的模型没配单价，或用的是会话累计用量的近似算法 */
  partial: boolean;
}

/**
 * 本会话的预估花费。
 *
 * 优先逐条消息计算（每条消息记着当时用的厂商和模型，中途切换模型也算得对）；
 * 恢复的历史会话没有逐条用量，退回到"会话累计用量 × 当前模型单价"的近似值。
 * 一个可计价的调用都没有时返回 null，状态栏不显示这一项。
 */
export function sessionCost(
  s: Pick<ChatState, "timeline" | "sessionUsage" | "provider" | "model">,
  profiles: ProfileInfo[],
): CostEstimate | null {
  let usd = 0;
  let priced = 0;
  let unpriced = 0;
  for (const it of assistantItems(s)) {
    if (!it.usage) continue;
    const c = costOf(it.usage, modelMeta(profiles, it.provider, it.model));
    if (c === null) unpriced++;
    else {
      usd += c;
      priced++;
    }
  }
  if (priced > 0) return { usd, partial: unpriced > 0 };
  if (unpriced === 0 && s.sessionUsage.input_tokens > 0) {
    const c = costOf(s.sessionUsage, modelMeta(profiles, s.provider, s.model));
    if (c !== null) return { usd: c, partial: true };
  }
  return null;
}

/** 运行中当前是第几步（第几次模型调用）。 */
export function currentStep(s: Pick<ChatState, "timeline" | "running">): number | null {
  if (!s.running) return null;
  const items = assistantItems(s);
  return items.length ? items[items.length - 1].step : null;
}

// ---- 格式化 ----

/** 12345 → "12.3k"，200000 → "200k"，1234567 → "1.23M"（去掉多余的 .0） */
export function fmtTokens(n: number): string {
  const trim = (x: string) => x.replace(/\.0+$/, "").replace(/(\.\d*?)0+$/, "$1");
  if (n >= 1e6) return `${trim((n / 1e6).toFixed(n >= 1e7 ? 1 : 2))}M`;
  if (n >= 1e4) return `${trim((n / 1e3).toFixed(1))}k`;
  return String(n);
}

/** 小额显示两位有效数字（$0.0043），否则保留两位小数。 */
export function fmtUSD(usd: number): string {
  if (usd === 0) return "$0";
  if (usd < 0.01) return `$${usd.toPrecision(2)}`;
  return `$${usd.toFixed(usd < 10 ? 2 : 1)}`;
}

/** 毫秒 → "8s"、"1m05s" */
export function fmtDuration(ms: number): string {
  const sec = Math.max(0, Math.round(ms / 1000));
  if (sec < 60) return `${sec}s`;
  return `${Math.floor(sec / 60)}m${String(sec % 60).padStart(2, "0")}s`;
}
