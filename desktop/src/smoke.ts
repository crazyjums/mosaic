import { readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { app, BrowserWindow } from "electron";

// 冒烟测试：在真实的渲染进程里验证整条链路——页面加载、React 渲染出来、window.mosaic 可用、
// 通过 IPC 调后端跑完一轮演示对话（含权限确认）。用 `npm run smoke` 运行（Linux 上用 xvfb-run）。

const SCRIPT = `
(async () => {
  const wait = (ms) => new Promise((r) => setTimeout(r, ms));
  for (let i = 0; i < 100 && !(document.getElementById("root")?.children.length); i++) await wait(100);
  const rendered = !!document.getElementById("root")?.children.length;
  const hasBridge = typeof window.mosaic?.call === "function";
  const counts = {};
  let done;
  const finished = new Promise((r) => (done = r));
  const off = window.mosaic.onEvent((ev) => {
    if (!ev) return;
    counts[ev.type] = (counts[ev.type] || 0) + 1;
    if (ev.type === "permission_request") window.mosaic.call("RespondPermission", [ev.data.request_id, "allow"]);
    if (ev.type === "turn_end") done(ev.data.reason);
  });
  const hello = await window.mosaic.call("GetVersion", []);
  const st = await window.mosaic.call("NewSession", []);
  await window.mosaic.call("SendToSession", [st.session_id, "帮我做个演示", []]);
  const reason = await Promise.race([finished, wait(60000).then(() => "timeout")]);
  off();
  let rejected = "";
  try { await window.mosaic.call("Startup", []); } catch (e) { rejected = String(e.message || e); }
  return { rendered, hasBridge, version: hello, reason, counts, rejected };
})()
`;

// 在带 CSP 的真实页面里，把 Mermaid / Graphviz(wasm) / KaTeX 的打包产物加载出来并真的渲染一次。
// 这些库是按需分包的，导出名被压缩过，所以按“有没有 render / instance / renderToString”来找。
const PROBE = (files: { mermaid: string; viz: string; katex: string }) => `
(async () => {
  const out = {};
  const load = (f) => import("/assets/" + f);
  try {
    const m = await load(${JSON.stringify(files.mermaid)});
    const mermaid = Object.values(m).find((v) => v && typeof v.render === "function" && typeof v.initialize === "function");
    mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: "default", flowchart: { htmlLabels: false } });
    out.mermaid = (await mermaid.render("smoke-mmd", "graph TD; A-->B")).svg.includes("<svg");
  } catch (e) { out.mermaid = "ERR " + (e && e.message || e); }
  try {
    const v = await load(${JSON.stringify(files.viz)});
    let svg = "";
    for (const f of Object.values(v)) {
      if (typeof f !== "function") continue;
      try { const inst = await f(); if (inst && typeof inst.renderString === "function") { svg = inst.renderString("digraph { a -> b }", { format: "svg" }); break; } } catch {}
    }
    out.viz = svg.includes("<svg");
  } catch (e) { out.viz = "ERR " + (e && e.message || e); }
  try {
    const k = await load(${JSON.stringify(files.katex)});
    const katex = Object.values(k).find((v) => v && typeof v.renderToString === "function");
    out.katex = katex.renderToString("x^2 + y^2", { throwOnError: true }).includes("katex");
  } catch (e) { out.katex = "ERR " + (e && e.message || e); }
  return out;
})()
`;

function findChunks(frontendDir: string): { mermaid: string; viz: string; katex: string } {
  const files = readdirSync(join(frontendDir, "assets"));
  const pick = (re: RegExp) => {
    const f = files.find((n) => re.test(n));
    if (!f) throw new Error(`前端产物里找不到 ${re}`);
    return f;
  };
  return { mermaid: pick(/^mermaid\.core-.*\.js$/), viz: pick(/^viz-.*\.js$/), katex: pick(/^katex-.*\.js$/) };
}

// 在真实页面里确认两件事：CSP 确实生效（eval 被拦），以及网页预览按设计工作
// （允许脚本时脚本能跑，不允许时跑不了）。放在 CSP 违规检查之后：这里故意触发的违规不算。
const POLICY_PROBE = `
(async () => {
  const out = {};
  try { new Function("return 1")(); out.evalBlocked = false; } catch { out.evalBlocked = true; }
  const store = async (html, scripts) => {
    const res = await fetch("mosaic-preview://store/" + (scripts ? "?scripts=1" : ""), { method: "POST", body: html, headers: { "content-type": "text/plain", "x-mosaic-token": window.mosaic.previewToken } });
    const text = await res.text();
    if (!res.ok) throw new Error("store " + res.status + " " + text);
    return JSON.parse(text).url;
  };
  const run = (url, sandbox) => new Promise((resolve) => {
    const f = document.createElement("iframe");
    f.setAttribute("sandbox", sandbox);
    const on = (e) => { if (e.data === "preview-ran") { cleanup(); resolve(true); } };
    const cleanup = () => { removeEventListener("message", on); f.remove(); };
    addEventListener("message", on);
    setTimeout(() => { cleanup(); resolve(false); }, 1500);
    f.src = url;
    document.body.appendChild(f);
  });
  const page = "<script>parent.postMessage('preview-ran','*')</script>";
  out.previewScripts = await run(await store(page, true), "allow-scripts");
  out.previewNoScripts = await run(await store(page, false), "");
  return out;
})()
`;

export async function runSmoke(win: BrowserWindow, cspViolations: string[] = [], frontendDir?: string): Promise<unknown> {
  // 从进程启动到页面加载完成（含启动后端、握手）的毫秒数：冷启动速度的参考。
  const loadMs = Math.round(process.uptime() * 1000);
  const r = (await win.webContents.executeJavaScript(SCRIPT, true)) as {
    rendered: boolean;
    hasBridge: boolean;
    reason: string;
    counts: Record<string, number>;
    rejected: string;
  };
  if (!r.rendered) throw new Error("React 界面没有渲染出来");
  if (!r.hasBridge) throw new Error("window.mosaic 不存在");
  if (r.reason !== "completed") throw new Error(`这一轮没有正常结束：${r.reason}`);
  if ((r.counts.permission_request ?? 0) < 2 || !r.counts.turn_end) throw new Error(`事件不完整：${JSON.stringify(r.counts)}`);
  if (!r.rejected.includes("没有方法")) throw new Error(`生命周期方法不应该能被调用：${r.rejected}`);
  // 带 CSP 的页面里，按需加载的图表库也要真的能用。
  let libs: Record<string, unknown> | undefined;
  if (frontendDir) {
    libs = (await win.webContents.executeJavaScript(PROBE(findChunks(frontendDir)), true)) as Record<string, unknown>;
    for (const [k, v] of Object.entries(libs)) if (v !== true) throw new Error(`${k} 在 CSP 下不能用：${String(v)}`);
  }
  if (cspViolations.length > 0) throw new Error(`出现了 CSP 违规：\n${cspViolations.join("\n")}`);
  const policy = (await win.webContents.executeJavaScript(POLICY_PROBE, true)) as { evalBlocked: boolean; previewScripts: boolean; previewNoScripts: boolean };
  if (!policy.evalBlocked) throw new Error("CSP 没有生效：eval 没有被拦住");
  if (!policy.previewScripts) throw new Error("允许脚本的网页预览里脚本没有运行");
  if (policy.previewNoScripts) throw new Error("不允许脚本的网页预览里脚本竟然运行了");
  // 多窗口：能打开独立的 Git 窗口（同一个后端、只渲染 Git 视图）；不在白名单里的窗口类型被拒绝。
  const badKind = (await win.webContents.executeJavaScript('window.mosaic.openWindow("shell").then(() => "opened", (e) => String(e.message || e))', true)) as string;
  if (badKind === "opened") throw new Error("不在白名单里的窗口类型不应该能打开");
  await win.webContents.executeJavaScript('window.mosaic.openWindow("git")', true);
  const aux = BrowserWindow.getAllWindows().find((w) => w !== win);
  if (!aux) throw new Error("没有打开独立的 Git 窗口");
  const auxState = (await (async () => {
    for (let i = 0; i < 100; i++) {
      const st = (await aux.webContents.executeJavaScript(
        '({ url: location.href, rendered: !!document.getElementById("root")?.children.length, text: document.body.innerText.slice(0, 200) })',
        true,
      )) as { url: string; rendered: boolean; text: string };
      if (st.rendered && st.text.trim()) return st;
      await new Promise((res) => setTimeout(res, 100));
    }
    throw new Error("独立的 Git 窗口没有渲染出来");
  })()) as { url: string; text: string };
  if (!auxState.url.includes("window=git")) throw new Error(`独立窗口地址不对：${auxState.url}`);
  if (/发送|新对话/.test(auxState.text)) throw new Error("独立 Git 窗口里不应该有聊天界面");
  const reopenCount = BrowserWindow.getAllWindows().length;
  await win.webContents.executeJavaScript('window.mosaic.openWindow("git")', true);
  if (BrowserWindow.getAllWindows().length !== reopenCount) throw new Error("同一种独立窗口应该只开一个");
  aux.close();

  // 独立的产物窗口：参数经壳校验，地址里带路径，窗口里渲染产物预览（文件不存在时显示读取失败，但路径和工具栏在）。
  const badArtifact = (await win.webContents.executeJavaScript(
    'window.mosaic.openWindow("artifact", { path: "/x/a.exe", workspace: "/x", kind: "exe" }).then(() => "opened", (e) => String(e.message || e))',
    true,
  )) as string;
  if (badArtifact === "opened") throw new Error("不合法的产物窗口参数不应该能打开");
  const art = '{ path: "/tmp/smoke ws/报告 & 图.html", workspace: "/tmp/smoke ws", kind: "html" }';
  const beforeArt = BrowserWindow.getAllWindows().length;
  await win.webContents.executeJavaScript(`window.mosaic.openWindow("artifact", ${art})`, true);
  let artWin: BrowserWindow | undefined;
  for (let i = 0; i < 50 && !artWin; i++) {
    artWin = BrowserWindow.getAllWindows().find((w) => w !== win && w.webContents.getURL().includes("window=artifact"));
    if (!artWin) await new Promise((res) => setTimeout(res, 100));
  }
  if (!artWin) throw new Error("没有打开独立的产物窗口");
  let artText = "";
  for (let i = 0; i < 100 && !artText.includes("报告 & 图.html"); i++) {
    artText = (await artWin.webContents.executeJavaScript("document.body.innerText", true)) as string;
    await new Promise((res) => setTimeout(res, 100));
  }
  if (!artText.includes("报告 & 图.html")) throw new Error(`独立产物窗口里没有显示文件路径：${artText.slice(0, 200)}`);
  if (/发送|新对话/.test(artText)) throw new Error("独立产物窗口里不应该有聊天界面");
  await win.webContents.executeJavaScript(`window.mosaic.openWindow("artifact", ${art})`, true);
  if (BrowserWindow.getAllWindows().length !== beforeArt + 1) throw new Error("同一个文件的产物窗口应该只开一个");
  await win.webContents.executeJavaScript('window.mosaic.openWindow("artifact", { path: "/tmp/smoke ws/other.md", workspace: "/tmp/smoke ws", kind: "markdown" })', true);
  if (BrowserWindow.getAllWindows().length !== beforeArt + 2) throw new Error("不同文件应该各开一个窗口");
  for (const w of BrowserWindow.getAllWindows()) if (w !== win) w.close();
  // 桌面设置：能读、能改；写法对但注册不了的快捷键被拒绝并原样保留旧设置。
  const cfg = (await win.webContents.executeJavaScript(
    `(async () => {
      const a = await window.mosaic.getShellSettings();
      const b = await window.mosaic.setShellSettings({ notifications: false, closeBehavior: "quit" });
      const bad = await window.mosaic.setShellSettings({ quickAsk: { shortcut: "Ctrl+Alt+Nonexistentkey" } });
      const junk = await window.mosaic.setShellSettings({ closeBehavior: "explode" });
      return { a, b, bad, junk };
    })()`,
    true,
  )) as { a: { closeBehavior: string; notifications: boolean }; b: { settings: { closeBehavior: string; notifications: boolean } }; bad: { settings: { quickAsk: { shortcut: string } }; error?: string }; junk: { settings: { closeBehavior: string } } };
  if (cfg.a.closeBehavior !== "tray-when-running" || !cfg.a.notifications) throw new Error(`默认的桌面设置不对：${JSON.stringify(cfg.a)}`);
  if (cfg.b.settings.closeBehavior !== "quit" || cfg.b.settings.notifications) throw new Error("桌面设置没有改成功");
  if (!cfg.bad.error || cfg.bad.settings.quickAsk.shortcut !== "CommandOrControl+Shift+M") throw new Error(`无效快捷键应该被拒绝并保持原样：${JSON.stringify(cfg.bad)}`);
  if (cfg.junk.settings.closeBehavior !== "quit") throw new Error("不合法的取值应该被丢弃，沿用当前值");
  // 内存：所有进程（主进程、渲染进程、GPU、后端 mosaicd 不在内）的工作集之和，单位 MB。
  const rss = app.getAppMetrics().reduce((n, m) => n + m.memory.workingSetSize, 0) / 1024;
  Object.assign(r, { libs, policy, aux: auxState.text.slice(0, 40), loadMs, memoryMB: Math.round(rss) });
  // 调试用：MOSAIC_SCREENSHOT=路径 时把当前窗口存成 PNG（肉眼确认真实窗口里渲染正常）。
  if (process.env.MOSAIC_SCREENSHOT) {
    await new Promise((res) => setTimeout(res, 500));
    writeFileSync(process.env.MOSAIC_SCREENSHOT, (await win.webContents.capturePage()).toPNG());
  }
  return r;
}
