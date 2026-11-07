import { randomBytes } from "node:crypto";
import { app, BrowserWindow, globalShortcut, ipcMain, net, Notification, protocol } from "electron";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, normalize, sep } from "node:path";
import { pathToFileURL } from "node:url";
import { Activity, alertText, type Alert } from "./activity";
import { handlePreview, PREVIEW_SCHEME, PreviewStore } from "./preview";
import { HarnessManager } from "./harness";
import { availableEngines, electronSpawner } from "./harness-electron";
import { createHostHandler, isWebURL, openExternal } from "./host";
import { resolvePaths } from "./paths";
import { Sidecar } from "./sidecar";
import { defaults as defaultSettings, normalize as normalizeSettings, SettingsFile, type ShellSettings } from "./settings";
import { auxWindowSpec } from "./windows";
import { createTray, type TrayHandle } from "./tray";
import { runE2E, runSmoke } from "./smoke";

// Mosaic 的 Electron 外壳：窗口、对话框、单实例；业务全在 Go 后端 mosaicd。
// 渲染进程（现有的 React 前端）只能通过 preload 的 window.mosaic 访问后端，这里按白名单转发。

const SMOKE = process.argv.includes("--smoke") || process.env.MOSAIC_SMOKE === "1";
const DEV_URL = process.env.MOSAIC_DEV_URL; // 开发时指向 vite 开发服务器（http://localhost:5173）
const APP_ORIGIN = "mosaic://app";

// 前端用自定义协议提供，而不是 file://：构建产物里的资源用的是绝对路径（/assets/…），
// 而且 ES 模块脚本在 file:// 下会被当成跨域；自定义协议下有正常的来源，将来也能给页面加 CSP。
protocol.registerSchemesAsPrivileged([
  { scheme: "mosaic", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } },
  { scheme: PREVIEW_SCHEME, privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } },
]);

// 应用页面的内容安全策略。脚本只能来自自己（打包的前端）；'wasm-unsafe-eval' 给 Graphviz（WebAssembly）；
// 样式要 'unsafe-inline'（KaTeX、Mermaid 和 React 的 style 属性）；图片允许 data:/blob:/https:（Markdown 里的网络图片）；
// 所有后端调用走 IPC，所以 connect-src 只留自己和预览协议；产物里的网页在 mosaic-preview:// 的沙箱 iframe 里（见 preview.ts）。
const CSP = [
  "default-src 'self'",
  "script-src 'self' 'wasm-unsafe-eval'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob: https:",
  "font-src 'self' data:",
  `connect-src 'self' ${PREVIEW_SCHEME}:`,
  "worker-src 'self' blob:",
  `frame-src ${PREVIEW_SCHEME}:`,
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
].join("; ");
// 存网页预览用的令牌：一次运行一个，只交给应用页面的 preload（见 preview.ts）。
const previewToken = randomBytes(32).toString("hex");
const cspViolations: string[] = []; // 冒烟测试用：页面里出现的 CSP 违规

let win: BrowserWindow | undefined;
let sidecar: Sidecar | undefined;
let tempHome: string | undefined;
let quitting = false;
let tray: TrayHandle | undefined;
let harnessMgr: HarnessManager | undefined;
let settings: ShellSettings = defaultSettings();
let settingsFile: SettingsFile | undefined;
let registeredShortcut: string | undefined;
const activity = new Activity();

const paths = resolvePaths({ packaged: app.isPackaged, resources: process.resourcesPath, appDir: app.getAppPath(), env: process.env });

function sidecarCommand(): { command: string; args: string[]; cwd?: string } {
  const args = ["-changelog", paths.changelog];
  if (SMOKE || process.env.MOSAIC_DEMO === "1") args.push("-demo");
  if (paths.mosaicd) return { command: paths.mosaicd, args };
  if (!paths.repo) throw new Error("找不到后端程序 mosaicd");
  // 开发时还没编译后端：直接 go run（启动慢一点，但不用先构建）。
  return { command: "go", args: ["run", "./cmd/mosaicd", ...args], cwd: paths.repo };
}

async function startSidecar(): Promise<Sidecar> {
  const { command, args, cwd } = sidecarCommand();
  const env = { ...process.env };
  if (SMOKE) {
    // 冒烟测试用临时目录当 HOME：不碰真实的 ~/.myllmapp。
    tempHome = mkdtempSync(join(tmpdir(), "mosaic-smoke-"));
    env.HOME = tempHome;
    env.USERPROFILE = tempHome;
  }
  const sc = new Sidecar({
    command,
    args,
    cwd,
    env,
    onHost: createHostHandler({ window: () => win, quit: () => app.quit(), harness: () => harnessMgr }),
  });
  sc.on("event", (data) => {
    const alert = activity.handle(data);
    if (alert) notify(alert);
    broadcast("mosaic:event", data);
  });
  sc.on("exit", (info: { code: number | null; expected: boolean }) => {
    if (quitting || info.expected) return;
    console.error(`后端意外退出（${info.code}）`);
    broadcast("mosaic:event", null); // 让界面知道：不再有事件了（界面收到 null 会忽略）
    if (!SMOKE) restartOnce();
  });
  await sc.start();
  // 引擎（Claude Code、pi 的 SDK）跑在 utilityProcess 里：把能提供的引擎告诉后端，选中对应配置档时它会通过我们运行。
  harnessMgr ??= new HarnessManager(electronSpawner(__dirname), {
    emit: (runId, messageId, type, data) => sidecar!.callInternal("harness.emit", { run_id: runId, message_id: messageId, type, data }).then(() => undefined),
    // 用 sidecar 的当前实例：后端重启后 sc 会换成新的。
    ask: (runId, req) => sidecar!.callInternal<"allow" | "allow_session" | "deny">("harness.ask", { run_id: runId, ...req }),
  });
  const engines = availableEngines(__dirname);
  if (engines.length > 0) await sc.callInternal("harness.register", { engines }).catch((e) => console.error("注册引擎失败：", e));
  return sc;
}

let restarts: number[] = [];
function restartOnce(): void {
  const now = Date.now();
  restarts = restarts.filter((t) => now - t < 60_000);
  if (restarts.length >= 2) {
    console.error("后端在一分钟内崩溃了两次，不再重试");
    return;
  }
  restarts.push(now);
  void startSidecar().then((sc) => (sidecar = sc)).catch((e) => console.error("重启后端失败：", e));
}

/** 事件发给所有窗口（主窗口和独立的 Git 窗口等）；各窗口自己按会话过滤。 */
function broadcast(channel: string, data: unknown): void {
  for (const w of BrowserWindow.getAllWindows()) if (!w.isDestroyed()) w.webContents.send(channel, data);
}

/** 让界面跳转：打开某个会话，或新建会话并聚焦输入框。只有主进程会发，渲染进程不能反过来指挥。 */
function navigate(target: { kind: "session"; id: string } | { kind: "new-session" }): void {
  win?.webContents.send("mosaic:navigate", target);
}

function showWindow(): void {
  if (!win || win.isDestroyed()) return;
  if (win.isMinimized()) win.restore();
  win.show();
  win.focus();
}

function quickAsk(): void {
  showWindow();
  navigate({ kind: "new-session" });
}

/** 按设置注册/注销全局快捷键；失败（被占用、格式无效）返回原因，已注册的保持注销状态由调用方恢复。 */
function applyShortcut(s: ShellSettings): string | undefined {
  if (registeredShortcut) {
    globalShortcut.unregister(registeredShortcut);
    registeredShortcut = undefined;
  }
  if (!s.quickAsk.enabled) {
    tray?.setShortcut(undefined);
    return undefined;
  }
  let ok = false;
  try {
    ok = globalShortcut.register(s.quickAsk.shortcut, quickAsk);
  } catch {
    ok = false;
  }
  if (!ok) {
    tray?.setShortcut(undefined);
    return `快捷键 ${s.quickAsk.shortcut} 已被其他程序占用，或者不是有效的组合`;
  }
  registeredShortcut = s.quickAsk.shortcut;
  tray?.setShortcut(registeredShortcut);
  return undefined;
}

/** 窗口不在眼前（隐藏、最小化、没有焦点）时才弹系统通知；点通知回到对应会话。 */
function notify(a: Alert): void {
  if (SMOKE || !settings.notifications || !Notification.isSupported()) return;
  if (win && !win.isDestroyed() && win.isVisible() && !win.isMinimized() && win.isFocused()) return;
  const n = new Notification({ ...alertText(a), silent: false });
  n.on("click", () => {
    showWindow();
    navigate({ kind: "session", id: a.sessionId });
  });
  n.show();
}

function isTrustedSender(url: string | undefined): boolean {
  if (!url) return false;
  return url.startsWith(APP_ORIGIN + "/") || (!!DEV_URL && url.startsWith(DEV_URL));
}

// 独立窗口：同一个后端、同一套页面，地址里的查询串让前端只渲染对应的视图。类型和参数的校验在 windows.ts。
const auxWindows = new Map<string, BrowserWindow>();
function openAuxWindow(kind: unknown, params: unknown): void {
  const spec = auxWindowSpec(kind, params);
  const existing = auxWindows.get(spec.key);
  if (existing && !existing.isDestroyed()) {
    if (existing.isMinimized()) existing.restore();
    existing.focus();
    return;
  }
  const w = createWindow({ title: spec.title, width: spec.width, height: spec.height, query: spec.query, aux: true });
  auxWindows.set(spec.key, w);
  w.on("closed", () => auxWindows.delete(spec.key));
}

function registerIpc(): void {
  ipcMain.handle("mosaic:shell-settings:get", (event) => {
    if (!isTrustedSender(event.senderFrame?.url)) throw new Error("拒绝来自不受信任页面的调用");
    return settings;
  });
  // 补丁里缺的字段沿用当前值，不合法的字段被丢弃；快捷键注册失败时回到原来的设置并把原因告诉界面。
  ipcMain.handle("mosaic:shell-settings:set", (event, patch: unknown) => {
    if (!isTrustedSender(event.senderFrame?.url)) throw new Error("拒绝来自不受信任页面的调用");
    const next = normalizeSettings(patch, settings);
    const err = applyShortcut(next);
    if (err) {
      applyShortcut(settings);
      return { settings, error: err };
    }
    settings = next;
    settingsFile?.save(settings);
    return { settings };
  });
  ipcMain.handle("mosaic:open-window", (event, kind: unknown, params: unknown) => {
    if (!isTrustedSender(event.senderFrame?.url)) throw new Error("拒绝来自不受信任页面的调用");
    openAuxWindow(kind, params);
  });
  ipcMain.on("mosaic:preview-token", (event) => {
    event.returnValue = isTrustedSender(event.senderFrame?.url) ? previewToken : "";
  });
  ipcMain.handle("mosaic:call", (event, method: unknown, args: unknown) => {
    // 只接受我们自己的页面发来的调用（不信任 iframe、被导航走的页面）。
    if (!isTrustedSender(event.senderFrame?.url) || typeof method !== "string" || !Array.isArray(args)) {
      throw new Error("拒绝来自不受信任页面的调用");
    }
    if (!sidecar) throw new Error("后端没有运行");
    return sidecar.call(method, args);
  });
}

/** mosaic://app/<路径> → 前端构建目录里的文件；不允许跳出这个目录。 */
function registerAppProtocol(): void {
  const root = normalize(paths.frontend + sep);
  protocol.handle("mosaic", async (req) => {
    const { pathname } = new URL(req.url);
    const rel = decodeURIComponent(pathname === "/" ? "/index.html" : pathname);
    const file = normalize(join(paths.frontend, rel));
    if (!file.startsWith(root)) return new Response("forbidden", { status: 403 });
    const res = await net.fetch(pathToFileURL(file).toString());
    // 只给页面本身加（开发时指向 vite 的页面不在这里）；脚本、样式等资源不需要。
    if (!(res.headers.get("content-type") ?? "").includes("text/html")) return res;
    const headers = new Headers(res.headers);
    headers.set("content-security-policy", CSP);
    return new Response(res.body, { status: res.status, headers });
  });
  const previews = new PreviewStore();
  protocol.handle(PREVIEW_SCHEME, (req) => handlePreview(req, previews, APP_ORIGIN, previewToken));
}

interface WindowOpts {
  title?: string;
  width?: number;
  height?: number;
  /** 地址后面的查询串，例如 "?window=git" */
  query?: string;
  /** 独立窗口：关闭就是关闭，不藏到托盘 */
  aux?: boolean;
}

function createWindow(opts: WindowOpts = {}): BrowserWindow {
  const w = new BrowserWindow({
    title: opts.title ?? "Mosaic",
    width: opts.width ?? 1280,
    height: opts.height ?? 840,
    minWidth: 900,
    minHeight: 600,
    backgroundColor: "#0f1115",
    show: !SMOKE,
    webPreferences: { preload: join(__dirname, "preload.js"), contextIsolation: true, sandbox: true, nodeIntegration: false },
  });
  if (SMOKE) {
    w.webContents.on("console-message", (e) => {
      if (/Content Security Policy/i.test(e.message)) cspViolations.push(e.message.slice(0, 300));
    });
  }
  // 页面里的链接一律交给系统浏览器（只放行 http / https），窗口本身不能被导航走、不能开新窗口。
  w.webContents.setWindowOpenHandler(({ url }) => {
    openExternal(url);
    return { action: "deny" };
  });
  w.webContents.on("will-navigate", (e, url) => {
    if (isTrustedSender(url)) return;
    e.preventDefault();
    if (isWebURL(url)) openExternal(url);
  });
  // 关窗时如果还有会话在跑：藏到托盘继续跑，而不是把任务一起杀掉；没有任务在跑就照常退出。
  let told = false;
  w.on("close", (e) => {
    if (opts.aux || quitting || SMOKE || !tray) return;
    // 设置 → 桌面 → 关闭窗口时：总是退出 / 有任务在跑才藏到托盘（默认）/ 总是藏到托盘
    const b = settings.closeBehavior;
    if (b === "quit" || (b === "tray-when-running" && activity.count === 0)) return;
    e.preventDefault();
    w.hide();
    if (!told && Notification.isSupported()) {
      told = true;
      new Notification({ title: "Mosaic 仍在后台运行", body: "任务完成后会通知你；点托盘图标可以回到窗口，退出请用托盘菜单。" }).show();
    }
  });
  void w.loadURL((DEV_URL ?? `${APP_ORIGIN}/`) + (opts.query ?? ""));
  return w;
}

async function main(): Promise<void> {
  if (!SMOKE && !app.requestSingleInstanceLock()) {
    // 已经有一个在运行：两个实例会同时打开同一个 sessions.db。
    app.quit();
    return;
  }
  app.on("second-instance", showWindow);
  await app.whenReady();
  registerAppProtocol();
  registerIpc();
  sidecar = await startSidecar();
  win = createWindow();
  // 主窗口真正关掉（不是藏到托盘）就退出整个应用，不让独立窗口留着一个没有主窗口的进程。
  win.on("closed", () => {
    if (!quitting) app.quit();
  });
  if (!SMOKE) {
    settingsFile = new SettingsFile(join(app.getPath("userData"), "shell-settings.json"));
    settings = settingsFile.load();
    tray = createTray(paths.icon, undefined, { show: showWindow, quickAsk, quit: () => app.quit() });
    const err = applyShortcut(settings);
    if (err) console.error(err + "，快速提问只能从托盘菜单进入");
  }
  if (SMOKE) {
    win.webContents.once("did-finish-load", () => {
      const e2eScript = process.env.MOSAIC_E2E_SCRIPT;
      const e2eOut = process.env.MOSAIC_E2E_OUT;
      if (e2eScript && e2eOut) {
        runE2E(win!, e2eScript, e2eOut).then(
          () => app.exit(0),
          (e: unknown) => {
            console.error("E2E FAIL", e instanceof Error ? e.message : e);
            app.exit(1);
          },
        );
        return;
      }
      runSmoke(win!, cspViolations, DEV_URL ? undefined : paths.frontend)
        .then((summary) => {
          console.log("SMOKE OK", JSON.stringify(summary));
          app.exit(0);
        })
        .catch((e: unknown) => {
          console.error("SMOKE FAIL", e instanceof Error ? e.message : e);
          app.exit(1);
        });
    });
  }
}

app.on("window-all-closed", () => app.quit());
app.on("will-quit", () => globalShortcut.unregisterAll());
app.on("before-quit", (e) => {
  if (quitting) return;
  quitting = true;
  harnessMgr?.killAll();
  if (sidecar?.running) {
    e.preventDefault();
    void sidecar.stop().finally(() => {
      if (tempHome) rmSync(tempHome, { recursive: true, force: true });
      app.quit();
    });
  }
});

main().catch((e) => {
  console.error("启动失败：", e);
  app.exit(1);
});
