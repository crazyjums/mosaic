import { describe, expect, it } from "vitest";
import { placeAbove } from "./popover";

const vp = { width: 1920, height: 1032 };

describe("placeAbove", () => {
  it("锚点靠近右边缘时，卡片向左收，不超出窗口", () => {
    const p = placeAbove({ left: 1830, top: 960 }, vp);
    expect(p.left + p.width).toBeLessThanOrEqual(vp.width - 8);
    expect(p.left).toBe(vp.width - p.width - 8);
  });
  it("锚点在左边时，与锚点左对齐", () => {
    expect(placeAbove({ left: 100, top: 960 }, vp).left).toBe(100);
  });
  it("窗口比卡片还窄时，宽度缩小并贴边", () => {
    const p = placeAbove({ left: 50, top: 400 }, { width: 200, height: 600 });
    expect(p.width).toBe(184);
    expect(p.left).toBe(8);
  });
  it("卡片放在锚点上方，高度不超过上方空间", () => {
    const p = placeAbove({ left: 0, top: 500 }, vp);
    expect(p.bottom).toBe(vp.height - 500 + 8);
    expect(p.maxHeight).toBe(484);
  });
});
