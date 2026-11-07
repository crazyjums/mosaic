import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";
import { Sidecar } from "../src/sidecar";

// 用真实的后端（go build ./cmd/mosaicd）验证壳这一侧的协议实现：握手、白名单、事件、反向请求、退出。
const repo = join(__dirname, "..", "..", "..");
const haveGo = spawnSync("go", ["version"]).status === 0;
let dir: string;
let bin: string;

before(() => {
  if (!haveGo) return;
  dir = mkdtempSync(join(tmpdir(), "mosaic-sidecar-"));
  bin = join(dir, process.platform === "win32" ? "mosaicd.exe" : "mosaicd");
  execFileSync("go", ["build", "-o", bin, "./cmd/mosaicd"], { cwd: repo, stdio: "inherit" });
});
after(() => dir && rmSync(dir, { recursive: true, force: true }));

function newSidecar(hostCalls: string[], answer = "/picked") {
  return new Sidecar({
    command: bin,
    args: ["-demo"],
    env: { ...process.env, HOME: dir, USERPROFILE: dir },
    onHost: (method) => {
      hostCalls.push(method);
      return method === "host.openDirectory" ? answer : null;
    },
  });
}

test("握手、白名单、一轮对话、反向请求、退出", { skip: !haveGo && "没有 go" }, async () => {
  const hostCalls: string[] = [];
  const sc = newSidecar(hostCalls);
  const hello = await sc.start();
  assert.equal(hello.protocol, 1);
  assert.ok(hello.methods.includes("SendToSession") && hello.methods.includes("ChooseDirectory"));
  assert.ok(!hello.methods.includes("Startup"), "生命周期方法不应暴露");

  // 白名单：后端没报告的方法，壳在转发之前就拒绝。
  await assert.rejects(sc.call("Startup", []), /没有方法/);
  await assert.rejects(sc.call("__proto__", []), /没有方法/);

  const types: string[] = [];
  const done = new Promise<string>((resolve) =>
    sc.on("event", (ev: { type: string; data: { request_id?: string; reason?: string } }) => {
      types.push(ev.type);
      if (ev.type === "permission_request") void sc.call("RespondPermission", [ev.data.request_id, "allow"]);
      if (ev.type === "turn_end") resolve(ev.data.reason!);
    }),
  );
  const st = (await sc.call("NewSession", [])) as { session_id: string };
  await sc.call("SendToSession", [st.session_id, "帮我做个演示", []]);
  assert.equal(await done, "completed");
  assert.ok(types.includes("tool_result") && types.includes("text_delta"));

  // 后端请壳弹对话框 → 壳回答 → 结果回到调用方。
  assert.equal(await sc.call("ChooseDirectory", [""]), "/picked");
  assert.deepEqual(hostCalls, ["host.openDirectory"]);

  // 关 stdin：后端自己收尾退出。
  const exit = new Promise<{ expected: boolean }>((r) => sc.once("exit", r));
  await sc.stop();
  assert.equal((await exit).expected, true);
  assert.equal(sc.running, false);
  await assert.rejects(sc.call("GetStatus", []), /没有方法|断开|没有运行/);
});

test("后端意外退出：等待中的调用失败，exit 事件标明不是我们停的", { skip: !haveGo && "没有 go" }, async () => {
  const sc = newSidecar([]);
  await sc.start();
  const exit = new Promise<{ expected: boolean }>((r) => sc.once("exit", r));
  process.kill((sc as unknown as { child: { pid: number } }).child.pid, "SIGKILL");
  assert.equal((await exit).expected, false);
  await assert.rejects(sc.call("GetStatus", []), /断开/);
});

test("找不到后端程序：启动失败有明确的错误", async () => {
  const sc = new Sidecar({ command: join(tmpdir(), "no-such-mosaicd"), args: [], onHost: () => null });
  sc.on("exit", () => {});
  await assert.rejects(sc.start(), /无法启动后端|断开/);
});
