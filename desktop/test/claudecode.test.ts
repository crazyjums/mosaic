import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, chmodSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { test } from "node:test";

import type { SdkMessage } from "../src/workers/cc-translate";
import { findClaude, runClaudeCode, sdkPermission, type Sdk, type SdkQuery } from "../src/workers/claudecode";
import type { FromWorker, RunSpec, ToWorker, WorkerIO } from "../src/workers/protocol";

const spec = (over: Partial<RunSpec> = {}): RunSpec => ({ engine: "claudecode", session_id: "s1", text: "写个文件", workspace: "/w", permission_mode: "default", ...over });

function fakeIO() {
  const posted: FromWorker[] = [];
  let cb: (m: ToWorker) => void = () => {};
  const io: WorkerIO = { post: (m) => posted.push(m), onMessage: (c) => (cb = c) };
  return { io, posted, send: (m: ToWorker) => cb(m) };
}

/** 假 SDK：记录收到的 options，用脚本函数产生消息流。 */
function fakeSdk(script: (options: Record<string, any>) => AsyncGenerator<SdkMessage>) {
  const calls: { prompt: string; options: Record<string, any> }[] = [];
  const sdk: Sdk = {
    query: (p) => {
      calls.push(p as never);
      const gen = script(p.options) as AsyncGenerator<SdkMessage> & SdkQuery;
      gen.close = () => void gen.return(undefined as never);
      return gen;
    },
  };
  return { sdk, calls };
}
const load = (sdk: Sdk) => async () => sdk;
const which = () => "/usr/bin/claude";

const result = (ok = true, extra: Partial<SdkMessage> = {}): SdkMessage => ({ type: "result", subtype: ok ? "success" : "error_during_execution", is_error: !ok, session_id: "sess-9", usage: { input_tokens: 3, output_tokens: 1 }, ...extra });

test("一轮正常结束：把参数交给 SDK，返回会话 ID 和用量", async () => {
  const { sdk, calls } = fakeSdk(async function* () {
    yield { type: "system", subtype: "init", session_id: "sess-9" };
    yield { type: "assistant", message: { id: "a", content: [{ type: "text", text: "好" }] }, parent_tool_use_id: null };
    yield result();
  });
  const { io, posted } = fakeIO();
  const out = await runClaudeCode(spec({ model: "claude-sonnet-5", resume: "old-sess", workspace: "/proj", profile: "我的cc" }), io, load(sdk), which);
  assert.equal(out.reason, "completed");
  assert.equal(out.resume, "sess-9");
  assert.equal(out.usage.input_tokens, 3);
  const o = calls[0].options;
  assert.equal(calls[0].prompt, "写个文件");
  assert.equal(o.cwd, "/proj");
  assert.equal(o.model, "claude-sonnet-5");
  assert.equal(o.resume, "old-sess");
  assert.equal(o.pathToClaudeCodeExecutable, "/usr/bin/claude");
  assert.equal(o.includePartialMessages, true);
  assert.equal(o.permissionMode, "default");
  assert.ok(posted.some((m) => m.type === "emit" && m.event_type === "text_delta"));
});

test("消息头显示用户给配置档起的名字", async () => {
  const { sdk } = fakeSdk(async function* () {
    yield { type: "stream_event", event: { type: "message_start", message: { id: "a", model: "m" } }, parent_tool_use_id: null };
    yield result();
  });
  const { io, posted } = fakeIO();
  await runClaudeCode(spec({ profile: "我的cc" }), io, load(sdk), which);
  const start = posted.find((m) => m.type === "emit" && m.event_type === "message_start") as Extract<FromWorker, { type: "emit" }>;
  assert.equal((start.data as { provider: string }).provider, "我的cc");
});

test("模型 default 不覆盖；配置档里指定了 command 就用它", async () => {
  const { sdk, calls } = fakeSdk(async function* () {
    yield result();
  });
  await runClaudeCode(spec({ model: "default", options: { command: "/opt/my-claude" } }), fakeIO().io, load(sdk), () => undefined);
  assert.equal(calls[0].options.model, undefined);
  assert.equal(calls[0].options.resume, undefined);
  assert.equal(calls[0].options.pathToClaudeCodeExecutable, "/opt/my-claude");
});

test("找不到 claude 命令：给出安装提示，不加载 SDK", async () => {
  let loaded = false;
  const out = await runClaudeCode(spec(), fakeIO().io, async () => {
    loaded = true;
    return fakeSdk(async function* () {}).sdk;
  }, () => undefined);
  assert.equal(out.reason, "error");
  assert.match(out.error!, /npm install -g @anthropic-ai\/claude-code/);
  assert.equal(loaded, false);
});

test("SDK 加载失败、SDK 抛错（带 stderr 末尾）、没有 result", async () => {
  let out = await runClaudeCode(spec(), fakeIO().io, async () => { throw new Error("Cannot find module"); }, which);
  assert.match(out.error!, /无法加载 Claude Agent SDK.*Cannot find module/);

  const { sdk } = fakeSdk(async function* (options) {
    options.stderr("boot\n");
    options.stderr("auth failed: token expired\n");
    throw new Error("process exited with code 1");
  });
  out = await runClaudeCode(spec(), fakeIO().io, load(sdk), which);
  assert.equal(out.reason, "error");
  assert.match(out.error!, /code 1[\s\S]*token expired/);

  const none = fakeSdk(async function* () {
    yield { type: "system", subtype: "init", session_id: "x" };
  });
  out = await runClaudeCode(spec(), fakeIO().io, load(none.sdk), which);
  assert.match(out.error!, /之前就结束/);

  const bad = fakeSdk(async function* () {
    yield result(false, { result: "Invalid API key · Please run /login" });
  });
  out = await runClaudeCode(spec(), fakeIO().io, load(bad.sdk), which);
  assert.equal(out.reason, "error");
  assert.match(out.error!, /login/);
});

test("审批：转给主进程，按回答允许 / 本会话允许 / 拒绝", async () => {
  const seen: unknown[] = [];
  const { sdk } = fakeSdk(async function* (options) {
    seen.push(await options.canUseTool("Write", { file_path: "/w/a.txt" }, { toolUseID: "t1", title: "写入 a.txt", suggestions: [{ type: "addRules" }] }));
    seen.push(await options.canUseTool("Bash", { command: "ls -la" }, { toolUseID: "t2" }));
    seen.push(await options.canUseTool("Read", { file_path: "/w/a.txt" }, { toolUseID: "t3", suggestions: [{ type: "addRules" }] }));
    yield result();
  });
  const { io, posted, send } = fakeIO();
  const answers = ["allow_session", "deny", "allow"] as const;
  let n = 0;
  const poll = setInterval(() => {
    for (const m of posted) {
      if (m.type === "ask" && !(m as any).answered) {
        (m as any).answered = true;
        send({ type: "answer", id: m.id, decision: answers[n++] });
      }
    }
  }, 5);
  const out = await runClaudeCode(spec(), io, load(sdk), which);
  clearInterval(poll);
  assert.equal(out.reason, "completed");
  const asks = posted.filter((m): m is Extract<FromWorker, { type: "ask" }> => m.type === "ask");
  assert.deepEqual(asks.map((a) => [a.request.tool, a.request.risk, a.request.call_id]), [["Write", "write", "t1"], ["Bash", "exec", "t2"], ["Read", "read", "t3"]]);
  assert.equal(asks[0].request.summary, "写入 a.txt");
  assert.equal(asks[1].request.summary, "Bash：ls -la", "没有 title 时自己概括");
  assert.deepEqual(seen[0], { behavior: "allow", updatedInput: { file_path: "/w/a.txt" }, updatedPermissions: [{ type: "addRules" }] });
  assert.deepEqual(seen[1], { behavior: "deny", message: "用户拒绝了这次操作", interrupt: false });
  assert.deepEqual(seen[2], { behavior: "allow", updatedInput: { file_path: "/w/a.txt" } }, "普通允许不带永久规则");
});

test("取消：中止 SDK，等待中的确认按拒绝处理，返回已有的内容", async () => {
  const { sdk, calls } = fakeSdk(async function* (options) {
    yield { type: "stream_event", event: { type: "message_start", message: { id: "a" } }, parent_tool_use_id: null };
    yield { type: "stream_event", event: { type: "content_block_delta", delta: { type: "text_delta", text: "我来" } }, parent_tool_use_id: null };
    const pending = options.canUseTool("Bash", { command: "rm -rf x" }, { toolUseID: "t1" }); // 用户一直没回答
    await new Promise((_, rej) => options.abortController.signal.addEventListener("abort", () => rej(new Error("aborted"))));
    await pending;
  });
  const { io, posted, send } = fakeIO();
  const run = runClaudeCode(spec(), io, load(sdk), which);
  while (!posted.some((m) => m.type === "ask")) await new Promise((r) => setTimeout(r, 5));
  send({ type: "cancel" });
  const out = await run;
  assert.equal(out.reason, "cancelled");
  assert.equal(calls[0].options.abortController.signal.aborted, true);
  const texts = posted.filter((m) => m.type === "emit" && m.event_type === "text_delta");
  assert.equal(texts.length, 1);
  assert.ok(posted.some((m) => m.type === "emit" && m.event_type === "message_end" && (m.data as any).stop_reason === "cancelled"));
});

test("权限模式映射", () => {
  assert.deepEqual(sdkPermission("readonly"), { permissionMode: "plan" });
  assert.deepEqual(sdkPermission("default"), { permissionMode: "default" });
  assert.deepEqual(sdkPermission("accept_edits"), { permissionMode: "acceptEdits" }, "输入是 Mosaic 序列化出来的 accept_edits");
  assert.deepEqual(sdkPermission("acceptEdits"), { permissionMode: "default" }, "SDK 的写法不是 Mosaic 的取值，按最严的默认处理");
  assert.deepEqual(sdkPermission("auto"), { permissionMode: "bypassPermissions", allowDangerouslySkipPermissions: true });
  assert.deepEqual(sdkPermission("whatever"), { permissionMode: "default" }, "不认识的模式按最严的默认处理");
});

test("在 PATH 里找 claude", () => {
  const dir = mkdtempSync(join(tmpdir(), "cc-path-"));
  const other = mkdtempSync(join(tmpdir(), "cc-empty-"));
  assert.equal(findClaude({ PATH: other }, "linux"), undefined);
  const f = join(dir, "claude");
  writeFileSync(f, "#!/bin/sh\n");
  chmodSync(f, 0o755);
  assert.equal(findClaude({ PATH: [other, dir].join(delimiter) }, "linux"), f);
  // Windows 上优先 .exe
  writeFileSync(join(dir, "claude.cmd"), "");
  writeFileSync(join(dir, "claude.exe"), "");
  assert.equal(findClaude({ PATH: dir }, "win32"), join(dir, "claude.exe"));
});
