import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { UpdateInfo } from "../bridge";
import { useChat } from "./chatStore";
import { shouldRecheck, updateIndicator, useUpdate } from "./updateStore";

const mocked = vi.hoisted(() => ({ checkUpdate: vi.fn(), downloadUpdate: vi.fn(), applyUpdate: vi.fn(), getUpdateStatus: vi.fn(), getVersion: vi.fn() }));
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
  it("下载完成后提示安装", () => {
    expect(updateIndicator({ info: info("1.1.0"), seen: "1.1.0", status: { state: "ready", done: 2, total: 2, version: "1.1.0" } }, "")).toBe("ready");
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


describe("两步更新：下载完成后询问，有会话在运行就等结束再装", () => {
  const ready = { state: "ready" as const, done: 10, total: 10, version: "1.2.0" };
  const reset = () => {
    useUpdate.setState({ info: info("1.2.0"), status: { state: "idle", done: 0, total: 0 }, dialogOpen: false, applyQueued: false, error: "" });
    useChat.setState({ running: false, sessionId: "s1", activity: {} });
  };

  beforeEach(() => {
    vi.useFakeTimers();
    for (const m of Object.values(mocked)) m.mockReset();
    mocked.downloadUpdate.mockResolvedValue(undefined);
    mocked.applyUpdate.mockResolvedValue(undefined);
    mocked.getVersion.mockResolvedValue("1.0.0");
    reset();
  });
  afterEach(() => {
    useUpdate.getState().cancelQueued();
    vi.useRealTimers();
  });

  it("下载在后台进行；下载完成时窗口重新弹出（即使被最小化了），并且不会自动安装", async () => {
    mocked.getUpdateStatus
      .mockResolvedValueOnce({ state: "downloading", done: 5, total: 10 })
      .mockResolvedValue(ready);
    await useUpdate.getState().download();
    useUpdate.getState().closeDialog(); // 用户把窗口最小化
    await vi.advanceTimersByTimeAsync(500);
    expect(useUpdate.getState().status.state).toBe("downloading");
    expect(useUpdate.getState().dialogOpen).toBe(false);
    await vi.advanceTimersByTimeAsync(500);
    expect(useUpdate.getState().status.state).toBe("ready");
    expect(useUpdate.getState().dialogOpen).toBe(true);
    expect(mocked.applyUpdate).not.toHaveBeenCalled();
  });

  it("稍后手动安装：关掉窗口后安装包仍然就绪，不会自己装", async () => {
    useUpdate.setState({ status: ready, dialogOpen: true });
    useUpdate.getState().closeDialog();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(useUpdate.getState().status.state).toBe("ready");
    expect(mocked.applyUpdate).not.toHaveBeenCalled();
  });

  it("没有会话在运行：立即安装", async () => {
    mocked.getUpdateStatus.mockResolvedValue({ state: "restarting", done: 10, total: 10 });
    useUpdate.setState({ status: ready });
    await useUpdate.getState().applyNow();
    expect(mocked.applyUpdate).toHaveBeenCalledTimes(1);
    expect(useUpdate.getState().applyQueued).toBe(false);
    expect(useUpdate.getState().status.state).toBe("installing");
  });

  it("当前会话在运行：排队，不调用后端；结束后再装", async () => {
    await useUpdate.getState().init();
    mocked.getUpdateStatus.mockResolvedValue({ state: "restarting", done: 10, total: 10 });
    useUpdate.setState({ status: ready });
    useChat.setState({ running: true });
    await useUpdate.getState().applyNow();
    expect(useUpdate.getState().applyQueued).toBe(true);
    await vi.advanceTimersByTimeAsync(9000);
    expect(mocked.applyUpdate).not.toHaveBeenCalled();
    useChat.setState({ running: false });
    await vi.advanceTimersByTimeAsync(0);
    expect(mocked.applyUpdate).toHaveBeenCalledTimes(1);
    expect(useUpdate.getState().applyQueued).toBe(false);
  });

  it("其他会话在后台运行也要等", async () => {
    useUpdate.setState({ status: ready });
    useChat.setState({ running: false, sessionId: "s1", activity: { s2: { running: true, waiting: 0, unread: false } } });
    await useUpdate.getState().applyNow();
    expect(mocked.applyUpdate).not.toHaveBeenCalled();
    expect(useUpdate.getState().applyQueued).toBe(true);
    useChat.setState({ activity: { s2: { running: false, waiting: 0, unread: true } } });
    await vi.advanceTimersByTimeAsync(3000); // 定时重试
    expect(mocked.applyUpdate).toHaveBeenCalledTimes(1);
  });

  it("后端说还有任务在跑：继续排队，之后重试；取消排队后不再尝试", async () => {
    mocked.applyUpdate.mockRejectedValueOnce(new Error("agent 正在运行，请等这一轮结束"));
    useUpdate.setState({ status: ready });
    await useUpdate.getState().applyNow();
    expect(useUpdate.getState().applyQueued).toBe(true);
    useUpdate.getState().cancelQueued();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(mocked.applyUpdate).toHaveBeenCalledTimes(1);
    expect(useUpdate.getState().applyQueued).toBe(false);
  });

  it("安装失败：不再排队，保留就绪状态和原因，可以重试", async () => {
    mocked.applyUpdate.mockRejectedValueOnce(new Error("磁盘已满"));
    useUpdate.setState({ status: ready });
    await useUpdate.getState().applyNow();
    const st = useUpdate.getState();
    expect(st.applyQueued).toBe(false);
    expect(st.status.state).toBe("ready");
    expect(st.status.error).toContain("磁盘已满");
  });

  it("下载失败：回到可操作状态并带原因", async () => {
    mocked.downloadUpdate.mockRejectedValueOnce(new Error("offline"));
    await useUpdate.getState().download();
    expect(useUpdate.getState().status.state).toBe("error");
    expect(useUpdate.getState().status.error).toContain("offline");
  });
});
