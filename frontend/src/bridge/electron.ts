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
  /** 桌面设置（关窗行为、通知、全局快捷键），由壳保存。旧版壳没有。 */
  getShellSettings?(): Promise<unknown>;
  setShellSettings?(patch: unknown): Promise<unknown>;
  /** 请壳打开一个独立窗口（目前只有 "git"）。 */
  openWindow?(kind: string, params?: unknown): Promise<void>;
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
export type ShellWindowKind = "git" | "artifact";

/** 独立的产物窗口要显示哪个文件：path 是绝对路径，workspace 是它所在的工作区，kind 是产物类型。 */
export interface ArtifactWindowParams {
  path: string;
  workspace: string;
  kind: string;
}

/** 当前页面是壳打开的哪种独立窗口（地址里的查询串）。 */
export type ShellWindow = { kind: "git" } | { kind: "artifact"; path: string; workspace: string; artifactKind: string };

const ARTIFACT_KINDS = ["html", "svg", "mermaid", "dot", "markdown", "image", "text", "other"];

/** 主窗口返回 null；独立窗口的参数形状不对也当作主窗口（宁可显示完整应用，也不渲染一个残缺的视图）。 */
export function shellWindow(search: string = typeof location === "undefined" ? "" : location.search): ShellWindow | null {
  const q = new URLSearchParams(search);
  const w = q.get("window");
  if (w === "git") return { kind: "git" };
  if (w === "artifact") {
    const path = q.get("path");
    const workspace = q.get("workspace");
    const artifactKind = q.get("kind");
    if (path && workspace && artifactKind && ARTIFACT_KINDS.includes(artifactKind)) return { kind: "artifact", path, workspace, artifactKind };
  }
  return null;
}

/** 在独立窗口里打开；不在 Electron 里（或壳太旧）时返回 false，调用方保留原来的做法。 */
export async function openShellWindow(kind: "git"): Promise<boolean>;
export async function openShellWindow(kind: "artifact", params: ArtifactWindowParams): Promise<boolean>;
export async function openShellWindow(kind: ShellWindowKind, params?: ArtifactWindowParams): Promise<boolean> {
  const b = bridge();
  if (!b?.openWindow) return false;
  await b.openWindow(kind, params);
  return true;
}

/** 壳的设置（desktop/src/settings.ts）。 */
export interface ShellSettings {
  closeBehavior: "tray-when-running" | "quit" | "tray";
  notifications: boolean;
  quickAsk: { enabled: boolean; shortcut: string };
}

const CLOSE_BEHAVIORS = ["tray-when-running", "quit", "tray"];

/** 壳发来的设置不可信到什么程度都不用管，但界面只依赖这几个字段，形状不对就当没有。 */
export function parseShellSettings(raw: unknown): ShellSettings | undefined {
  const r = raw as Partial<ShellSettings> | null;
  if (!r || !CLOSE_BEHAVIORS.includes(r.closeBehavior as string) || typeof r.notifications !== "boolean") return undefined;
  const q = r.quickAsk;
  if (!q || typeof q.enabled !== "boolean" || typeof q.shortcut !== "string") return undefined;
  return { closeBehavior: r.closeBehavior!, notifications: r.notifications, quickAsk: { enabled: q.enabled, shortcut: q.shortcut } };
}

/** 读桌面设置；不在 Electron 里（或壳太旧）时返回 null，设置页据此不显示“桌面”标签。 */
export async function getShellSettings(): Promise<ShellSettings | null> {
  const b = bridge();
  if (!b?.getShellSettings) return null;
  return parseShellSettings(await b.getShellSettings()) ?? null;
}

/** 保存桌面设置的一部分。返回生效后的完整设置；快捷键被占用之类的失败原因在 error 里（设置保持原样）。 */
export async function setShellSettings(patch: Partial<Omit<ShellSettings, "quickAsk">> & { quickAsk?: Partial<ShellSettings["quickAsk"]> }): Promise<{ settings: ShellSettings; error?: string }> {
  const b = bridge();
  if (!b?.setShellSettings) throw new Error("当前的桌面壳不支持桌面设置");
  const r = (await b.setShellSettings(patch)) as { settings?: unknown; error?: unknown };
  const settings = parseShellSettings(r?.settings);
  if (!settings) throw new Error("桌面壳返回了无法识别的设置");
  return { settings, error: typeof r.error === "string" ? r.error : undefined };
}
