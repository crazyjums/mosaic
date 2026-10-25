// 用量页的格式化：花费（美元 / 人民币）、百分比、日期。

import type { UsageNode } from "../bridge";
import { fmtTokens } from "./metrics";

export type Currency = "usd" | "cny";

/** 把美元花费格式化成当前币种。内部一律是美元，人民币按用户填的汇率换算。 */
export function fmtCost(usd: number, currency: Currency, rate: number): string {
  const v = currency === "cny" ? usd * rate : usd;
  const sym = currency === "cny" ? "¥" : "$";
  if (v === 0) return `${sym}0`;
  if (v < 0.01) return `<${sym}0.01`;
  return `${sym}${v < 100 ? v.toFixed(2) : Math.round(v).toLocaleString()}`;
}

/** 叶子节点的花费列：套餐类显示"套餐"，没有价格的显示"—"（不是 0：没有价格不等于免费）。 */
export function nodeCost(n: UsageNode, currency: Currency, rate: number, plan: string): string {
  if (n.children?.length) {
    // 汇总节点：有花费就显示；全是套餐或没有价格时显示对应的标记
    if (n.cost_usd > 0) return fmtCost(n.cost_usd, currency, rate) + (n.unpriced_calls > 0 ? "+" : "");
    if (n.plan_calls > 0 && n.unpriced_calls === 0) return plan;
    return "—";
  }
  if (n.plan) return plan;
  if (!n.priced) return "—";
  return fmtCost(n.cost_usd, currency, rate);
}

export const fmtPct = (x: number): string => `${Math.round(x * 100)}%`;

/** 2026-10-25 → "10-25 周日"。无法解析时原样返回。 */
export function fmtDay(day: string, weekdays: string[]): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (!m) return day;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  return `${m[2]}-${m[3]} ${weekdays[d.getDay()] ?? ""}`.trim();
}

export { fmtTokens };
