import { afterEach, describe, expect, it, vi } from "vitest";
import { createElectronBackend, isElectron } from "./electron";

// window.mosaic 是 Electron preload 注入的；这里用假的验证“按方法名、按位置传参”的约定，以及事件订阅。
function install(call: (m: string, a: unknown[]) => Promise<unknown>) {
  const listeners = new Set<(d: unknown) => void>();
  (globalThis as unknown as { window: unknown }).window = {
    mosaic: { call, onEvent: (cb: (d: unknown) => void) => (listeners.add(cb), () => listeners.delete(cb)), platform: "linux" },
  };
  return { emit: (d: unknown) => listeners.forEach((l) => l(d)), count: () => listeners.size };
}

afterEach(() => {
  delete (globalThis as unknown as { window?: unknown }).window;
});

describe("Electron 桥接", () => {
  it("没有 window.mosaic 时不是 Electron", () => {
    (globalThis as unknown as { window: unknown }).window = {};
    expect(isElectron()).toBe(false);
  });

  it("方法名和参数顺序与 Wails 绑定一致，null 列表变成空数组", async () => {
    const call = vi.fn(async (m: string) => (m === "ListSessions" ? null : { ok: true }));
    install(call);
    expect(isElectron()).toBe(true);
    const b = createElectronBackend();
    expect(b.kind).toBe("electron");
    expect(await b.listSessions()).toEqual([]);
    await b.send("sess_1", "你好", undefined);
    expect(call).toHaveBeenCalledWith("SendToSession", ["sess_1", "你好", []]);
    await b.setModel("anthropic", "m1");
    expect(call).toHaveBeenCalledWith("SetModel", ["anthropic", "m1"]);
    await b.cancel();
    expect(call).toHaveBeenCalledWith("Cancel", []);
    await b.cancel("sess_2");
    expect(call).toHaveBeenCalledWith("CancelSession", ["sess_2"]);
  });

  it("后端的错误原样变成 Promise 的拒绝", async () => {
    install(async () => {
      throw new Error("agent 正在运行");
    });
    await expect(createElectronBackend().installUpdate()).rejects.toThrow("agent 正在运行");
  });

  it("事件：按 seq 重排后交付，认识的类型才交付，可以取消订阅", async () => {
    const h = install(async () => null);
    const got: number[] = [];
    const off = createElectronBackend().onEvent((ev) => got.push(ev.seq));
    const ev = (seq: number, type = "text_delta") => ({ v: 1, type, id: `e${seq}`, seq, session_id: "s", agent_id: "root", data: {} });
    h.emit(ev(1));
    h.emit(ev(3)); // 先到的 3 要等 2
    h.emit(ev(2));
    h.emit(ev(4, "某个以后才有的类型")); // 不认识的类型被忽略
    await new Promise((r) => setTimeout(r, 200));
    expect(got).toEqual([1, 2, 3]);
    off();
    expect(h.count()).toBe(0);
  });
});
