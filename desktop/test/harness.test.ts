import assert from "node:assert/strict";
import { test } from "node:test";

import { HarnessManager, type GoLink, type WorkerHandle } from "../src/harness";
import { parseRunSpec } from "../src/host";
import type { AskRequest, FromWorker, Outcome, RunSpec, ToWorker } from "../src/workers/protocol";

const spec: RunSpec = { engine: "claudecode", session_id: "s1", text: "你好", workspace: "/w", permission_mode: "default" };
const done = (over: Partial<Outcome> = {}): Outcome => ({ reason: "completed", usage: { input_tokens: 1, output_tokens: 2 }, ...over });

class FakeWorker implements WorkerHandle {
  sent: ToWorker[] = [];
  killed = 0;
  private msg: (m: FromWorker) => void = () => {};
  private exit: (c: number | null) => void = () => {};
  private err: (c: string) => void = () => {};
  post(m: ToWorker) { this.sent.push(m); }
  onMessage(cb: (m: FromWorker) => void) { this.msg = cb; }
  onExit(cb: (c: number | null) => void) { this.exit = cb; }
  onStderr(cb: (c: string) => void) { this.err = cb; }
  kill() { this.killed++; }
  // 测试用：模拟 worker 一侧
  fromWorker(m: FromWorker) { this.msg(m); }
  die(code: number | null) { this.exit(code); }
  stderr(c: string) { this.err(c); }
}

function setup(go: Partial<GoLink> = {}, graceMs = 50) {
  const w = new FakeWorker();
  const log: string[] = [];
  const link: GoLink = {
    emit: async (runId, messageId, type) => { log.push(`emit ${runId} ${messageId} ${type}`); },
    ask: async () => "allow",
    ...go,
  };
  const mgr = new HarnessManager(() => w, link, graceMs);
  return { w, mgr, log };
}

const tick = (ms = 5) => new Promise((r) => setTimeout(r, ms));

test("把 run 交给 worker；事件按顺序送到 Go，结束前等它们送完", async () => {
  const delays = [30, 1, 10]; // 前面的事件故意送得更慢：顺序也不能乱
  const order: string[] = [];
  const { w, mgr } = setup({
    emit: async (_r, _m, type) => {
      await tick(delays.shift()!);
      order.push(type);
    },
  });
  const p = mgr.run("r1", spec);
  assert.deepEqual(w.sent[0], { type: "run", spec });
  w.fromWorker({ type: "emit", message_id: "m1", event_type: "message_start", data: {} });
  w.fromWorker({ type: "emit", message_id: "m1", event_type: "text_delta", data: {} });
  w.fromWorker({ type: "emit", message_id: "m1", event_type: "message_end", data: {} });
  w.fromWorker({ type: "done", outcome: done({ resume: "x" }) });
  const out = await p;
  assert.deepEqual(order, ["message_start", "text_delta", "message_end"], "顺序送达，且都送完了才返回结果");
  assert.equal(out.resume, "x");
  assert.equal(w.killed, 1, "结束后清理 worker");
});

test("确认请求：转给 Go，用户的回答转回 worker；不挡住后面的事件", async () => {
  let answer!: (d: "allow" | "allow_session" | "deny") => void;
  const asked: AskRequest[] = [];
  const { w, mgr, log } = setup({
    ask: (_r, req) => { asked.push(req); return new Promise((res) => (answer = res)); },
  });
  const p = mgr.run("r1", spec);
  w.fromWorker({ type: "ask", id: 7, request: { call_id: "c1", tool: "Bash", risk: "exec", summary: "ls" } });
  w.fromWorker({ type: "emit", message_id: "", event_type: "tool_result", data: {} });
  await tick(20);
  assert.equal(asked.length, 1);
  assert.ok(log.some((l) => l.includes("tool_result")), "等用户回答期间其他事件照常送达");
  assert.ok(!w.sent.some((m) => m.type === "answer"), "还没回答");
  answer("allow_session");
  await tick(10);
  assert.deepEqual(w.sent[w.sent.length - 1], { type: "answer", id: 7, decision: "allow_session" });
  w.fromWorker({ type: "done", outcome: done() });
  await p;
});

test("向 Go 请求确认失败（比如已被取消）：按拒绝回给 worker", async () => {
  const { w, mgr } = setup({ ask: async () => { throw new Error("cancelled"); } });
  const p = mgr.run("r1", spec);
  w.fromWorker({ type: "ask", id: 1, request: { call_id: "c", tool: "Bash", risk: "exec", summary: "x" } });
  await tick(10);
  assert.deepEqual(w.sent[w.sent.length - 1], { type: "answer", id: 1, decision: "deny" });
  w.fromWorker({ type: "done", outcome: done() });
  await p;
});

test("取消：通知 worker，等它交回结果", async () => {
  const { w, mgr } = setup({}, 500);
  const p = mgr.run("r1", spec);
  mgr.cancel("r1");
  assert.deepEqual(w.sent[w.sent.length - 1], { type: "cancel" });
  w.fromWorker({ type: "done", outcome: done({ reason: "cancelled", messages: [{ role: "assistant" }] }) });
  const out = await p;
  assert.equal(out.reason, "cancelled");
  assert.equal(out.messages?.length, 1, "取消时已有的内容被保留");
});

test("取消后 worker 不响应：宽限期过了强制结束，按已取消返回", async () => {
  const { w, mgr } = setup({}, 30);
  const p = mgr.run("r1", spec);
  mgr.cancel("r1");
  const out = await p;
  assert.equal(out.reason, "cancelled");
  assert.equal(w.killed, 1);
});

test("worker 没交结果就退出：报错并带上 stderr 末尾", async () => {
  const { w, mgr } = setup();
  const p = mgr.run("r1", spec);
  w.stderr("loading\nTypeError: x is not a function\n");
  w.die(1);
  const out = await p;
  assert.equal(out.reason, "error");
  assert.match(out.error!, /意外退出（1）[\s\S]*TypeError/);
});

test("引擎不可用（起不来 worker）", async () => {
  const mgr = new HarnessManager(() => undefined, { emit: async () => {}, ask: async () => "allow" });
  const out = await mgr.run("r1", spec);
  assert.equal(out.reason, "error");
  assert.match(out.error!, /claudecode.*不可用/);
});

test("结果只处理一次；结束后取消是空操作；killAll 结束所有 worker", async () => {
  const { w, mgr } = setup();
  const p = mgr.run("r1", spec);
  w.fromWorker({ type: "done", outcome: done() });
  w.die(0); // done 之后的退出不能再算作崩溃
  const out = await p;
  assert.equal(out.reason, "completed");
  mgr.cancel("r1"); // 已经结束了
  assert.equal(w.sent.filter((m) => m.type === "cancel").length, 0);

  const s2 = setup();
  void s2.mgr.run("r2", spec);
  s2.mgr.killAll();
  assert.equal(s2.w.killed, 1);
});

test("校验 Go 发来的请求", () => {
  assert.deepEqual(parseRunSpec({ ...spec, resume: "", model: "", profile: "cc" }), { ...spec, profile: "cc", options: undefined, resume: undefined, model: undefined });
  assert.deepEqual(parseRunSpec({ ...spec, options: { command: "/x" }, text: "" }).options, { command: "/x" });
  for (const bad of [null, {}, { ...spec, engine: "" }, { ...spec, workspace: 3 }, { ...spec, session_id: undefined }, { ...spec, permission_mode: "" }, { ...spec, text: 5 }]) {
    assert.throws(() => parseRunSpec(bad), /不合法/, JSON.stringify(bad)?.slice(0, 50));
  }
  assert.equal(parseRunSpec({ ...spec, options: [1, 2] }).options, undefined, "options 必须是对象");
});
