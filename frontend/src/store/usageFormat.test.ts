import { describe, expect, it } from "vitest";
import type { UsageNode } from "../bridge";
import { fmtCost, fmtDay, nodeCost } from "./usageFormat";

const node = (p: Partial<UsageNode>): UsageNode => ({
  key: "k", label: "l", priced: true, input: 0, output: 0, cache_read: 0, cache_write: 0, requests: 1, failed_tries: 0, handed_on: 0,
  cost_usd: 0, unpriced_calls: 0, plan_calls: 0, cache_hit: 0, ...p,
});

describe("用量格式化", () => {
  it("美元和人民币", () => {
    expect(fmtCost(1.234, "usd", 7.2)).toBe("$1.23");
    expect(fmtCost(1, "cny", 7.2)).toBe("¥7.20");
    expect(fmtCost(0.001, "usd", 7.2)).toBe("<$0.01");
    expect(fmtCost(0, "usd", 7.2)).toBe("$0");
    expect(fmtCost(1234.5, "usd", 7.2)).toMatch(/^\$1,?235$/);
  });
  it("没有价格显示 —，不是 0；套餐显示套餐", () => {
    expect(nodeCost(node({ priced: false }), "usd", 7, "套餐")).toBe("—");
    expect(nodeCost(node({ plan: true, priced: false }), "usd", 7, "套餐")).toBe("套餐");
    expect(nodeCost(node({ cost_usd: 2 }), "usd", 7, "套餐")).toBe("$2.00");
  });
  it("汇总节点：部分没有价格时加 + 号提示花费不完整", () => {
    const parent = node({ cost_usd: 2, unpriced_calls: 3, children: [node({})] });
    expect(nodeCost(parent, "usd", 7, "套餐")).toBe("$2.00+");
    expect(nodeCost(node({ plan_calls: 2, children: [node({})] }), "usd", 7, "套餐")).toBe("套餐");
    expect(nodeCost(node({ unpriced_calls: 2, children: [node({})] }), "usd", 7, "套餐")).toBe("—");
  });
  it("日期带星期", () => {
    expect(fmtDay("2026-10-25", ["周日", "周一", "周二", "周三", "周四", "周五", "周六"])).toBe("10-25 周日");
    expect(fmtDay("not a date", [])).toBe("not a date");
  });
});
