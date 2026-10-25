import { describe, expect, it } from "vitest";
import type { UpdateInfo } from "../bridge";
import { shouldShowBanner } from "./updateStore";

const info = (latest: string, available = true): UpdateInfo => ({ current: "1.0.0", latest, available, manual: false });
const idle = { state: "idle" as const, done: 0, total: 0 };

describe("更新横幅", () => {
  it("有新版本才显示", () => {
    expect(shouldShowBanner({ info: info("1.1.0"), dismissed: false, status: idle }, "")).toBe(true);
    expect(shouldShowBanner({ info: info("1.0.0", false), dismissed: false, status: idle }, "")).toBe(false);
    expect(shouldShowBanner({ info: null, dismissed: false, status: idle }, "")).toBe(false);
  });
  it("稍后和跳过", () => {
    expect(shouldShowBanner({ info: info("1.1.0"), dismissed: true, status: idle }, "")).toBe(false);
    expect(shouldShowBanner({ info: info("1.1.0"), dismissed: false, status: idle }, "1.1.0")).toBe(false);
    // 跳过的是旧版本，更新的版本仍然提示
    expect(shouldShowBanner({ info: info("1.2.0"), dismissed: false, status: idle }, "1.1.0")).toBe(true);
  });
  it("安装中一直显示", () => {
    expect(shouldShowBanner({ info: info("1.1.0"), dismissed: true, status: { state: "downloading", done: 1, total: 2 } }, "1.1.0")).toBe(true);
  });
});
