import { writeFileSync } from "node:fs";
import type { BrowserWindow } from "electron";

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

export async function runSmoke(win: BrowserWindow): Promise<unknown> {
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
  // 调试用：MOSAIC_SCREENSHOT=路径 时把当前窗口存成 PNG（肉眼确认真实窗口里渲染正常）。
  if (process.env.MOSAIC_SCREENSHOT) {
    await new Promise((res) => setTimeout(res, 500));
    writeFileSync(process.env.MOSAIC_SCREENSHOT, (await win.webContents.capturePage()).toPNG());
  }
  return r;
}
