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
  /** 壳要求界面跳转（点通知、全局快捷键）。旧版壳没有这个函数。 */
  onNavigate?(cb: (target: unknown) => void): () => void;
  /** 请壳打开一个独立窗口（目前只有 "git"）。 */
  openWindow?(kind: string): Promise<void>;
  /** 存网页预览用的令牌，只有应用页面的 preload 能拿到。 */
  previewToken?: string;
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

/** 壳发来的跳转请求：打开某个会话，或新建会话并聚焦输入框。 */
export type ShellTarget = { kind: "session"; id: string } | { kind: "new-session" };

/** 只认这两种形状，其余一律忽略。 */
export function parseShellTarget(raw: unknown): ShellTarget | undefined {
  const t = raw as { kind?: unknown; id?: unknown } | null;
  if (t?.kind === "new-session") return { kind: "new-session" };
  if (t?.kind === "session" && typeof t.id === "string" && t.id) return { kind: "session", id: t.id };
  return undefined;
}

/** 订阅壳的跳转请求；不在 Electron 里（或壳太旧）时什么也不做。返回取消订阅函数。 */
export function onShellNavigate(handler: (t: ShellTarget) => void): () => void {
  const b = bridge();
  if (!b?.onNavigate) return () => undefined;
  return b.onNavigate((raw) => {
    const t = parseShellTarget(raw);
    if (t) handler(t);
  });
}

/**
 * 把网页产物交给壳，换回一个 mosaic-preview:// 地址放进沙箱 iframe。
 * 为什么不用 srcdoc：iframe 会继承应用页面的 CSP（不允许内联脚本），“允许脚本”的预览就跑不起来；
 * 走壳提供的独立来源，隔离靠响应头里的 CSP sandbox（见 desktop/src/preview.ts）。
 */
export async function storePreview(html: string, scripts: boolean): Promise<string> {
  const res = await fetch(`mosaic-preview://store/${scripts ? "?scripts=1" : ""}`, {
    method: "POST",
    body: html,
    headers: { "content-type": "text/plain", "x-mosaic-token": bridge()?.previewToken ?? "" },
  });
  if (!res.ok) throw new Error(await res.text());
  return ((await res.json()) as { url: string }).url;
}

/** 壳能打开的独立窗口。 */
export type ShellWindowKind = "git";

/** 当前页面是不是壳打开的独立窗口（地址里带 ?window=git）；主窗口返回 null。 */
export function shellWindowKind(search: string = typeof location === "undefined" ? "" : location.search): ShellWindowKind | null {
  return new URLSearchParams(search).get("window") === "git" ? "git" : null;
}

/** 在独立窗口里打开；不在 Electron 里（或壳太旧）时返回 false，调用方保留原来的浮层。 */
export async function openShellWindow(kind: ShellWindowKind): Promise<boolean> {
  const b = bridge();
  if (!b?.openWindow) return false;
  await b.openWindow(kind);
  return true;
}
