import { app, BrowserWindow, ipcMain, net, protocol } from "electron";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, normalize, sep } from "node:path";
import { pathToFileURL } from "node:url";
import { createHostHandler, isWebURL, openExternal } from "./host";
import { resolvePaths } from "./paths";
import { Sidecar } from "./sidecar";
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
  sc.on("event", (data) => win?.webContents.send("mosaic:event", data));
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
  void w.loadURL(DEV_URL ?? `${APP_ORIGIN}/`);
  return w;
}

async function main(): Promise<void> {
  if (!SMOKE && !app.requestSingleInstanceLock()) {
    // 已经有一个在运行：两个实例会同时打开同一个 sessions.db。
    app.quit();
    return;
  }
  app.on("second-instance", () => {
    if (win?.isMinimized()) win.restore();
    win?.focus();
  });
  await app.whenReady();
  registerAppProtocol();
  registerIpc();
  sidecar = await startSidecar();
  win = createWindow();
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
