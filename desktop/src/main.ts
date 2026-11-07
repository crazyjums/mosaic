import { app, BrowserWindow, globalShortcut, ipcMain, net, Notification, protocol, type Tray } from "electron";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, normalize, sep } from "node:path";
import { pathToFileURL } from "node:url";
import { Activity, alertText, type Alert } from "./activity";
import { createHostHandler, isWebURL, openExternal } from "./host";
import { resolvePaths } from "./paths";
import { Sidecar } from "./sidecar";
import { createTray } from "./tray";
import { runSmoke } from "./smoke";

// Mosaic 的 Electron 外壳：窗口、对话框、单实例；业务全在 Go 后端 mosaicd。
// 渲染进程（现有的 React 前端）只能通过 preload 的 window.mosaic 访问后端，这里按白名单转发。

const SMOKE = process.argv.includes("--smoke") || process.env.MOSAIC_SMOKE === "1";
const DEV_URL = process.env.MOSAIC_DEV_URL; // 开发时指向 vite 开发服务器（http://localhost:5173）
const APP_ORIGIN = "mosaic://app";

// 前端用自定义协议提供，而不是 file://：构建产物里的资源用的是绝对路径（/assets/…），
// 而且 ES 模块脚本在 file:// 下会被当成跨域；自定义协议下有正常的来源，将来也能给页面加 CSP。
protocol.registerSchemesAsPrivileged([{ scheme: "mosaic", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } }]);

let win: BrowserWindow | undefined;
let sidecar: Sidecar | undefined;
let tempHome: string | undefined;
let quitting = false;
let tray: Tray | undefined;
const activity = new Activity();
// 全局快捷键：随时唤起窗口并新建会话、聚焦输入框。可用环境变量改，设成空串关闭。
const QUICK_ASK = process.env.MOSAIC_QUICK_SHORTCUT ?? "CommandOrControl+Shift+M";

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
    onHost: createHostHandler({ window: () => win, quit: () => app.quit() }),
  });
  sc.on("event", (data) => {
    const alert = activity.handle(data);
    if (alert) notify(alert);
    win?.webContents.send("mosaic:event", data);
  });
  sc.on("exit", (info: { code: number | null; expected: boolean }) => {
    if (quitting || info.expected) return;
    console.error(`后端意外退出（${info.code}）`);
    win?.webContents.send("mosaic:event", null); // 让界面知道：不再有事件了（界面收到 null 会忽略）
    if (!SMOKE) restartOnce();
  });
  await sc.start();
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

/** 窗口不在眼前（隐藏、最小化、没有焦点）时才弹系统通知；点通知回到对应会话。 */
function notify(a: Alert): void {
  if (SMOKE || !Notification.isSupported()) return;
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

function registerIpc(): void {
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
  protocol.handle("mosaic", (req) => {
    const { pathname } = new URL(req.url);
    const rel = decodeURIComponent(pathname === "/" ? "/index.html" : pathname);
    const file = normalize(join(paths.frontend, rel));
    if (!file.startsWith(root)) return new Response("forbidden", { status: 403 });
    return net.fetch(pathToFileURL(file).toString());
  });
}

function createWindow(): BrowserWindow {
  const w = new BrowserWindow({
    title: "Mosaic",
    width: 1280,
    height: 840,
    minWidth: 900,
    minHeight: 600,
    backgroundColor: "#0f1115",
    show: !SMOKE,
    webPreferences: { preload: join(__dirname, "preload.js"), contextIsolation: true, sandbox: true, nodeIntegration: false },
  });
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
    if (quitting || SMOKE || !tray || activity.count === 0) return;
    e.preventDefault();
    w.hide();
    if (!told && Notification.isSupported()) {
      told = true;
      new Notification({ title: "Mosaic 仍在后台运行", body: "任务完成后会通知你；点托盘图标可以回到窗口，退出请用托盘菜单。" }).show();
    }
  });
  void w.loadURL(DEV_URL ?? `${APP_ORIGIN}/`);
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
  if (!SMOKE) {
    tray = createTray(paths.icon, QUICK_ASK || undefined, { show: showWindow, quickAsk, quit: () => app.quit() });
    if (QUICK_ASK && !globalShortcut.register(QUICK_ASK, quickAsk)) console.error(`全局快捷键 ${QUICK_ASK} 被占用，快速提问只能从托盘菜单进入`);
  }
  if (SMOKE) {
    win.webContents.once("did-finish-load", () => {
      runSmoke(win!)
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
