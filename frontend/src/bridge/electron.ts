// Electron 桌面端桥接：通过 preload 暴露的 window.mosaic 调用 Go 后端（mosaicd），监听后端推来的事件。
//
// 方法映射表和 Wails 版共用（wails.ts 的 createBackendFrom）：后端方法名、参数顺序、返回值约定都和 Wails 绑定一致，
// 所以这里只需要把“按名字调用”接到 IPC 上。协议见 docs/design/electron-migration.md。

import { createBackendFrom, type EventRuntime, type GoApp } from "./wails";
import type { Backend } from "./types";

/** preload 暴露的接口（desktop/src/preload.ts）。 */
interface MosaicBridge {
  call(method: string, args: unknown[]): Promise<unknown>;
  /** 订阅后端事件，返回取消订阅函数。 */
  onEvent(cb: (data: unknown) => void): () => void;
  platform: string;
}

function bridge(): MosaicBridge | undefined {
  return (window as unknown as { mosaic?: MosaicBridge }).mosaic;
}

/** Electron 的 preload 会在页面加载前注入 window.mosaic。 */
export function isElectron(): boolean {
  return typeof bridge()?.call === "function";
}

export function createElectronBackend(): Backend {
  const b = bridge()!;
  // 每个方法名对应一个按位置传参的调用；Proxy 让 GoApp 里的 100 多个方法不用逐个写一遍。
  const app = new Proxy({} as GoApp, {
    get: (_target, name) => (typeof name === "string" ? (...args: unknown[]) => b.call(name, args) : undefined),
  });
  const runtime: EventRuntime = { EventsOn: (_name, cb) => b.onEvent((data) => cb(data)) };
  return createBackendFrom(app, runtime, "electron");
}
