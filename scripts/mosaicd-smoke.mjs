#!/usr/bin/env node
// 从外部用 Node 驱动 mosaicd 跑一轮对话：这正是 Electron 主进程将来要做的事（docs/design/electron-migration.md 阶段 1）。
//
// 用法：node scripts/mosaicd-smoke.mjs [mosaicd 可执行文件]
//   不传路径时用 `go run ./cmd/mosaicd`。用临时目录当 HOME，不碰真实的 ~/.myllmapp；使用离线演示厂商，不需要密钥。
//   成功时退出码 0，并打印收到的事件类型统计。
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const home = mkdtempSync(join(tmpdir(), "mosaicd-smoke-"));
const bin = process.argv[2];
const [cmd, args] = bin ? [bin, ["-demo"]] : ["go", ["run", "./cmd/mosaicd", "-demo"]];
const child = spawn(cmd, args, { env: { ...process.env, HOME: home, USERPROFILE: home }, stdio: ["pipe", "pipe", "inherit"] });

let nextId = 1;
const pending = new Map();
const counts = {};
let finish;
const done = new Promise((resolve) => (finish = resolve));

function send(msg) {
  child.stdin.write(JSON.stringify({ jsonrpc: "2.0", ...msg }) + "\n");
}
function call(method, params = []) {
  const id = nextId++;
  send({ id, method, params });
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
}

// 只按 "\n" 切分：不用 readline，它会把 JSON 字符串里的 U+2028 等字符也当成换行。
let buf = "";
child.stdout.setEncoding("utf8");
child.stdout.on("data", (chunk) => {
  buf += chunk;
  let i;
  while ((i = buf.indexOf("\n")) >= 0) {
    const line = buf.slice(0, i);
    buf = buf.slice(i + 1);
    if (line) onMessage(JSON.parse(line));
  }
});

function onMessage(m) {
  if (!m.method) {
    const p = pending.get(m.id);
    pending.delete(m.id);
    if (p) (m.error ? p.reject(new Error(m.error.message)) : p.resolve(m.result));
    return;
  }
  if (m.method === "event") {
    const ev = m.params.data;
    counts[ev.type] = (counts[ev.type] ?? 0) + 1;
    if (ev.type === "permission_request") call("RespondPermission", [ev.data.request_id, "allow"]);
    if (ev.type === "turn_end") finish(ev.data.reason);
    return;
  }
  // 反向请求：真正的壳会弹对话框；冒烟测试里直接回答“用户取消”。
  if (m.id !== undefined) send({ id: m.id, result: "" });
}

const timer = setTimeout(() => {
  console.error("超时");
  child.kill();
  process.exit(1);
}, 120_000);

try {
  const hello = await call("rpc.hello");
  console.log(`协议 ${hello.protocol}，版本 ${hello.version}，${hello.methods.length} 个方法`);
  const st = await call("NewSession");
  await call("SendToSession", [st.session_id, "帮我做个演示", []]);
  const reason = await done;
  console.log("事件：", counts);
  if (reason !== "completed") throw new Error(`这一轮没有正常结束：${reason}`);
  console.log("通过");
} catch (e) {
  console.error("失败：", e.message);
  process.exitCode = 1;
} finally {
  clearTimeout(timer);
  child.stdin.end(); // stdin EOF：mosaicd 自己退出
  await new Promise((r) => child.on("exit", r));
  rmSync(home, { recursive: true, force: true });
}
