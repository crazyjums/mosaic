// 手动端到端验证（不在 CI 里跑：要真实的 claude 命令和它的登录/API Key，会真的调用一次模型）。
//
//   node scripts/harness-e2e.mjs <Electron 可执行文件或 release/linux-unpacked/mosaic-desktop>
//
// 启动（打包后的）应用，在页面里通过 window.mosaic 添加一个 claudecode 厂商，建会话、发一句话，
// 打印整轮收到的事件，验证：壳注册了引擎 → Go 发 host.harness.run → utilityProcess 里的 SDK 跑起来 → 事件和审批回到界面。
import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const app = process.argv[2];
if (!app) throw new Error("用法：node scripts/harness-e2e.mjs <应用可执行文件>");
const home = mkdtempSync(join(tmpdir(), "mosaic-e2e-"));
const ws = mkdtempSync(join(tmpdir(), "mosaic-e2e-ws-"));
const resultFile = join(home, "result.json");
const prompt = process.env.E2E_PROMPT || "用 Write 工具在当前目录创建文件 hello.txt，内容是 hi，然后一句话告诉我。";

const script = `
(async () => {
  const wait = (ms) => new Promise((r) => setTimeout(r, ms));
  for (let i = 0; i < 100 && !(document.getElementById("root")?.children.length); i++) await wait(100);
  const events = [];
  const off = window.mosaic.onEvent((ev) => { if (ev) events.push(ev); });
  let ended;
  const finished = new Promise((r) => (ended = r));
  window.mosaic.onEvent((ev) => {
    if (!ev) return;
    if (ev.type === "permission_request") window.mosaic.call("RespondPermission", [ev.data.request_id, "allow"]);
    if (ev.type === "turn_end") ended(ev.data.reason);
  });
  const out = { steps: [] };
  const step = async (name, fn) => { try { out.steps.push([name, await fn()]); } catch (e) { out.steps.push([name, "ERR " + (e.message || e)]); } };
  await step("saveProvider", () => window.mosaic.call("SaveProvider", [{ original_name: "", name: "cc", type: "claudecode", base_url: "", api_key: "", models: ["default"], default_model: "default", options: {} }]).then(() => "ok"));
  await step("setWorkspace", () => Promise.resolve("workspace set below"));
  const st = await window.mosaic.call("NewSession", []);
  await step("setSessionWorkspace", () => window.mosaic.call("SetSessionWorkspace", [${JSON.stringify(ws)}]).then(() => "ok"));
  await step("setModel", () => window.mosaic.call("SetModel", ["cc", "default"]).then(() => "ok"));
  await step("send", () => window.mosaic.call("SendToSession", [st.session_id, ${JSON.stringify(prompt)}, []]).then(() => "ok"));
  out.reason = await Promise.race([finished, wait(180000).then(() => "timeout")]);
  if (out.reason === "completed" && ${JSON.stringify(process.env.E2E_PROMPT2 || "")}) {
    // 第二轮：验证恢复（引擎要记得上一轮做过什么）
    const mark = events.length;
    const second = new Promise((r) => (ended = r));
    await window.mosaic.call("SendToSession", [st.session_id, ${JSON.stringify(process.env.E2E_PROMPT2 || "")}, []]);
    out.reason2 = await Promise.race([second, wait(180000).then(() => "timeout")]);
    out.text2 = events.slice(mark).filter((e) => e.type === "text_delta").map((e) => e.data.text).join("");
  }
  off();
  out.events = events.map((e) => ({ type: e.type, data: e.data }));
  return out;
})()
`;
// 复用应用自带的冒烟模式入口：设置 MOSAIC_E2E_SCRIPT，由 smoke.ts 执行并把结果写到 MOSAIC_E2E_OUT
const env = { ...process.env, HOME: home, USERPROFILE: home, MOSAIC_E2E_SCRIPT: script, MOSAIC_E2E_OUT: resultFile, MOSAIC_SMOKE: "1", MOSAIC_E2E: "1" };
const child = spawn(process.platform === "linux" ? "xvfb-run" : app, process.platform === "linux" ? ["-a", app, "--no-sandbox"] : [], { env, stdio: "inherit" });
child.on("exit", (code) => {
  if (!existsSync(resultFile)) {
    console.error("没有结果文件，退出码", code);
    process.exit(1);
  }
  const r = JSON.parse(readFileSync(resultFile, "utf8"));
  console.log("steps:", JSON.stringify(r.steps));
  console.log("reason:", r.reason, r.reason2 ?? "");
  if (r.text2) console.log("第二轮回答:", r.text2);
  for (const e of r.events) console.log(" ", e.type, JSON.stringify(e.data).slice(0, 160));
  process.exit(r.reason === "completed" ? 0 : 1);
});
