import { describe, expect, it } from "vitest";
import { fmtReset, planName, remaining, remainingTone, windowLabel, windowShort } from "./quota";

describe("quota", () => {
  it("labels windows by length", () => {
    expect(windowLabel(18000)).toBe("5 小时用量上限");
    expect(windowLabel(604800)).toBe("每周限额");
    expect(windowLabel(86400 * 30)).toBe("30 天限额");
    expect(windowShort(18000)).toBe("5h");
    expect(windowShort(604800)).toBe("周");
  });

  it("remaining percent and tone", () => {
    expect(remaining({ seconds: 1, used_percent: 3.4 })).toBe(97);
    expect(remaining({ seconds: 1, used_percent: 120 })).toBe(0);
    expect(remainingTone(97)).toBe("ok");
    expect(remainingTone(30)).toBe("warn");
    expect(remainingTone(10)).toBe("danger");
  });

  it("formats reset time", () => {
    const now = Date.parse("2026-10-25T10:00:00Z");
    expect(fmtReset("2026-10-25T10:30:00Z", now)).toBe("30 分钟后重置");
    expect(fmtReset("2026-10-25T12:05:00Z", now)).toBe("2 小时 5 分钟后重置");
    expect(fmtReset("2026-10-25T09:00:00Z", now)).toBe("即将重置");
    expect(fmtReset("2026-10-28T10:00:00Z", now)).toMatch(/重置$/);
    expect(fmtReset(undefined, now)).toBe("");
    expect(planName("plus")).toBe("Plus");
  });
});
