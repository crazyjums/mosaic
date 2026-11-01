import { beforeEach, describe, expect, it, vi } from "vitest";
import type { UpdateInfo } from "../bridge";
import { shouldRecheck, updateIndicator, useUpdate } from "./updateStore";

const mocked = vi.hoisted(() => ({ checkUpdate: vi.fn() }));
vi.mock("../bridge", () => ({ backend: () => mocked }));

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

describe("后台更新检查", () => {
  beforeEach(() => {
    mocked.checkUpdate.mockReset();
    useUpdate.setState({
      info: null,
      checking: false,
      error: "",
      autoError: "",
      lastAttemptAt: 0,
      lastCheckedAt: 0,
    });
  });

  it("自动检查失败不会弹错误，但保留诊断信息供 UI 展示", async () => {
    mocked.checkUpdate.mockRejectedValueOnce(new Error("network unreachable"));
    await useUpdate.getState().check(false);
    const state = useUpdate.getState();
    expect(state.checking).toBe(false);
    expect(state.error).toBe("");
    expect(state.autoError).toContain("network unreachable");
    expect(state.lastCheckedAt).toBe(0);
    expect(state.lastAttemptAt).toBeGreaterThan(0);
  });

  it("后台检查成功后清除旧错误，并高亮新版本", async () => {
    mocked.checkUpdate.mockRejectedValueOnce(new Error("offline"));
    mocked.checkUpdate.mockResolvedValueOnce(info("1.2.0"));
    await useUpdate.getState().check(false);
    await useUpdate.getState().check(false);
    const state = useUpdate.getState();
    expect(state.autoError).toBe("");
    expect(state.lastCheckedAt).toBeGreaterThan(0);
    expect(updateIndicator(state, "")).toBe("blink");
  });

  it("手动检查失败显示错误，不将其误报为新版本", async () => {
    mocked.checkUpdate.mockRejectedValueOnce(new Error("connection failed"));
    await useUpdate.getState().check(true);
    const state = useUpdate.getState();
    expect(state.error).toContain("connection failed");
    expect(state.autoError).toContain("connection failed");
    expect(updateIndicator(state, "")).toBe("none");
  });

  it("后端禁用自动更新时应明确显示原因，而非静默当作最新版本", async () => {
    mocked.checkUpdate.mockResolvedValueOnce({ current: "dev", latest: "", available: false, manual: false, disabled: "开发版本不检查更新" });
    await useUpdate.getState().check(false);
    const state = useUpdate.getState();
    expect(state.info?.disabled).toContain("开发版本");
    expect(state.autoError).toContain("开发版本");
    expect(state.lastCheckedAt).toBe(0);
    expect(updateIndicator(state, "")).toBe("none");
  });

  it("窗口恢复时按时间限流，首次检查与间隔到期允许执行", () => {
    expect(shouldRecheck(0, 1000, 600000)).toBe(true);
    expect(shouldRecheck(1000, 1001, 600000)).toBe(false);
    expect(shouldRecheck(1000, 600999, 600000)).toBe(false);
    expect(shouldRecheck(1000, 601000, 600000)).toBe(true);
  });
});
