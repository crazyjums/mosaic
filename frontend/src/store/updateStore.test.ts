import { describe, expect, it } from "vitest";
import type { UpdateInfo } from "../bridge";
import { updateIndicator } from "./updateStore";

const info = (latest: string, available = true): UpdateInfo => ({ current: "1.0.0", latest, available, manual: false });
const idle = { state: "idle" as const, done: 0, total: 0 };

describe("更新按钮状态", () => {
  it("有新版本且没看过：闪烁", () => {
    expect(updateIndicator({ info: info("1.1.0"), seen: "", status: idle }, "")).toBe("blink");
  });
  it("没有新版本或还没检查：普通入口", () => {
    expect(updateIndicator({ info: info("1.0.0", false), seen: "", status: idle }, "")).toBe("none");
    expect(updateIndicator({ info: null, seen: "", status: idle }, "")).toBe("none");
  });
  it("看过之后不再闪，但保持高亮", () => {
    expect(updateIndicator({ info: info("1.1.0"), seen: "1.1.0", status: idle }, "")).toBe("steady");
    // 又出了更新的版本：重新闪
    expect(updateIndicator({ info: info("1.2.0"), seen: "1.1.0", status: idle }, "")).toBe("blink");
  });
  it("跳过的版本不提示，更新的版本照常提示", () => {
    expect(updateIndicator({ info: info("1.1.0"), seen: "", status: idle }, "1.1.0")).toBe("none");
    expect(updateIndicator({ info: info("1.2.0"), seen: "", status: idle }, "1.1.0")).toBe("blink");
  });
  it("安装中显示进度，出错后回到可操作状态", () => {
    expect(updateIndicator({ info: info("1.1.0"), seen: "1.1.0", status: { state: "downloading", done: 1, total: 2 } }, "1.1.0")).toBe("busy");
    expect(updateIndicator({ info: info("1.1.0"), seen: "1.1.0", status: { state: "error", done: 0, total: 0, error: "x" } }, "")).toBe("steady");
  });
});
