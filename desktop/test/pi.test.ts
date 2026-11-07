import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { needsAsk, runPi, type PiSdk, type PiSession } from "../src/workers/pi";
import { PiTranslator, piRisk, type PiEvent } from "../src/workers/pi-translate";
import type { FromWorker, RunSpec, ToWorker, WorkerIO } from "../src/workers/protocol";

const spec = (over: Partial<RunSpec> = {}): RunSpec => ({ engine: "pi", session_id: "s1", text: "写个文件", workspace: "/w", permission_mode: "default", ...over });

function fakeIO() {
  const posted: FromWorker[] = [];
  let cb: (m: ToWorker) => void = () => {};
  const io: WorkerIO = { post: (m) => posted.push(m), onMessage: (c) => (cb = c) };
  return { io, posted, send: (m: ToWorker) => cb(m) };
}

// 下面的事件形状来自对真实 pi 1.5.0 的探测（假的 OpenAI 兼容服务器 + 真 pi）。
const usage = { input: 10, output: 5, cacheRead: 1, cacheWrite: 2 };
const assistantTool = { role: "assistant", content: [{ type: "text", text: "好的，写文件" }, { type: "toolCall", id: "call_1", name: "write", arguments: { path: "a.txt", content: "hi" } }], provider: "fake", model: "fake-1", usage, stopReason: "toolUse" };
const assistantDone = { role: "assistant", content: [{ type: "text", text: "完成" }], provider: "fake", model: "fake-1", usage: { input: 20, output: 2 }, stopReason: "stop" };

function toolTurn(): PiEvent[] {
  return [
    { type: "agent_start" },
    { type: "message_start", message: { role: "system", content: "" } },
    { type: "message_end", message: { role: "system", content: "" } },
    { type: "message_start", message: { role: "user", content: [{ type: "text", text: "写个文件" }] } },
    { type: "message_end", message: { role: "user", content: [{ type: "text", text: "写个文件" }] } },
    { type: "message_start", message: { ...assistantTool, usage: {}, stopReason: "pending" } },
    { type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "好的，写文件" } },
    { type: "message_update", assistantMessageEvent: { type: "toolcall_end", toolCall: { id: "call_1", name: "write", arguments: { path: "a.txt", content: "hi" } } } },
    { type: "message_end", message: assistantTool },
    { type: "tool_execution_start", toolCallId: "call_1", toolName: "write", args: {} },
    { type: "tool_execution_end", toolCallId: "call_1", toolName: "write", result: { content: [{ type: "text", text: "Successfully wrote to a.txt" }] }, isError: false, durationMs: 3 },
    { type: "message_start", message: { role: "toolResult", toolCallId: "call_1" } },
    { type: "message_end", message: { role: "toolResult", toolCallId: "call_1" } },
    { type: "message_start", message: { role: "assistant", content: [], model: "fake-1" } },
    { type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "完成" } },
    { type: "message_end", message: assistantDone },
    { type: "agent_settled", aborted: false },
  ];
}

function collect(events: PiEvent[], model = "") {
  const out: { id: string; type: string; data: any }[] = [];
  const tr = new PiTranslator({ emit: (id, type, data) => out.push({ id, type, data }) }, "我的pi", model);
  for (const e of events) tr.handle(e);
  return { out, tr };
}

test("翻译：带工具的两步——system/user 不转发，工具结果、用量、历史都对", () => {
  const { out, tr } = collect(toolTurn());
  assert.deepEqual(
    out.map((e) => e.type),
    ["message_start", "text_delta", "tool_call", "message_end", "tool_result", "message_start", "text_delta", "message_end"],
  );
  assert.equal(out[0].data.provider, "我的pi");
  assert.equal(out[0].data.step, 1);
  assert.equal(out[2].data.risk, "write");
  assert.equal(out[3].data.stop_reason, "tool_use");
  assert.deepEqual(out[3].data.usage, { input_tokens: 10, output_tokens: 5, cache_read_tokens: 1, cache_write_tokens: 2 });
  assert.equal(out[4].data.output, "Successfully wrote to a.txt");
  assert.equal(out[4].id, "");
  assert.notEqual(out[0].id, out[5].id);
  assert.equal(out[0].id, out[1].id);
  const r = tr.finish("completed", "", "/s/x.jsonl");
  assert.equal(r.resume, "/s/x.jsonl");
  assert.deepEqual(r.usage, { input_tokens: 30, output_tokens: 7, cache_read_tokens: 1, cache_write_tokens: 2 });
  assert.deepEqual(
    r.messages!.map((m: any) => [m.role, m.content.map((b: any) => b.type)]),
    [
      ["assistant", ["text", "tool_use"]],
      ["user", ["tool_result"]],
      ["assistant", ["text"]],
    ],
  );
});

test("翻译：思考增量；被拒绝的工具调用（isError）也有结果", () => {
  const { out } = collect([
    { type: "message_start", message: { role: "assistant" } },
    { type: "message_update", assistantMessageEvent: { type: "thinking_delta", delta: "想想" } },
    { type: "message_update", assistantMessageEvent: { type: "toolcall_end", toolCall: { id: "c", name: "bash", arguments: { command: "ls" } } } },
    { type: "message_end", message: { role: "assistant", content: [{ type: "thinking", thinking: "想想" }, { type: "toolCall", id: "c", name: "bash", arguments: { command: "ls" } }], stopReason: "toolUse" } },
    { type: "tool_execution_end", toolCallId: "c", toolName: "bash", result: { content: [{ type: "text", text: "用户拒绝了这次操作" }] }, isError: true },
  ]);
  assert.equal(out[1].type, "thinking_delta");
  assert.equal(out[2].data.risk, "exec");
  const res = out.find((e) => e.type === "tool_result")!;
  assert.equal(res.data.is_error, true);
});

test("翻译：中途结束时补齐没有结果的工具调用", () => {
  const events = toolTurn().slice(0, 9); // 到 message_end（带 toolCall）为止
  const { out, tr } = collect(events);
  const r = tr.finish("cancelled");
  const last = out[out.length - 1];
  assert.equal(last.type, "tool_result");
  assert.equal(last.data.is_error, true);
  assert.equal((r.messages as any[])[1].content[0].type, "tool_result");
});

test("翻译：被中止的助手消息以 cancelled 结束；模型错误记在 lastError", () => {
  const { out, tr } = collect([
    { type: "message_start", message: { role: "assistant" } },
    { type: "message_update", assistantMessageEvent: { type: "text_delta", delta: "写到一半" } },
    { type: "message_end", message: { role: "assistant", content: [{ type: "text", text: "写到一半" }], stopReason: "aborted" } },
  ]);
  assert.equal(out[2].data.stop_reason, "cancelled");
  assert.deepEqual((tr.finish("cancelled").messages as any[])[0].content, [{ type: "text", text: "写到一半" }]);

  const e = collect([
    { type: "message_start", message: { role: "assistant" } },
    { type: "message_end", message: { role: "assistant", content: [], stopReason: "error", errorMessage: "401 invalid key" } },
  ]);
  assert.equal(e.tr.lastError, "401 invalid key");
});

test("风险与确认规则", () => {
  assert.equal(piRisk("read"), "read");
  assert.equal(piRisk("grep"), "read");
  assert.equal(piRisk("edit"), "write");
  assert.equal(piRisk("bash"), "exec");
  assert.equal(piRisk("某个扩展的工具"), "exec");
  assert.equal(needsAsk("default", "read"), false);
  assert.equal(needsAsk("default", "write"), true);
  assert.equal(needsAsk("accept_edits", "write"), false);
  assert.equal(needsAsk("accept_edits", "exec"), true);
  assert.equal(needsAsk("auto", "exec"), false);
  assert.equal(needsAsk("readonly", "write"), true);
});

// ---- worker ----

type Handler = (e: { toolName: string; toolCallId: string; input: Record<string, unknown> }) => Promise<{ block: boolean; reason?: string } | undefined>;

/** 假 pi SDK：记录 createAgentSession 的参数；prompt 时先走扩展的 tool_call 钩子，再放出脚本事件。 */
function fakeSdk(opts: { available?: number; models?: Record<string, unknown>; script?: (s: { emit(e: PiEvent): void; gate(tool: string, input: Record<string, unknown>): Promise<boolean> }) => Promise<void>; promptError?: Error } = {}) {
  const calls: Record<string, any>[] = [];
  const opened: string[] = [];
  let handler: Handler | undefined;
  let aborted = false;
  const sdk: PiSdk = {
    ModelRuntime: {
      create: async () => ({
        getModel: (p: string, id: string) => opts.models?.[`${p}/${id}`],
        getAvailable: async () => Array.from({ length: opts.available ?? 1 }, () => ({ provider: "fake", id: "fake-1" })),
      }),
    },
    SessionManager: { create: (cwd: string) => ({ kind: "create", cwd }), open: (p: string) => (opened.push(p), { kind: "open", path: p }) },
    DefaultResourceLoader: class {
      constructor(o: Record<string, unknown>) {
        const factory = (o.extensionFactories as ((pi: { on(ev: string, h: Handler): void }) => void)[])[0];
        factory({ on: (_ev, h) => (handler = h) });
      }
      async reload() {}
    } as never,
    getAgentDir: () => "/agent",
    createAgentSession: async (o) => {
      calls.push(o);
      let cb: (e: PiEvent) => void = () => {};
      const session: PiSession = {
        sessionFile: "/sessions/new.jsonl",
        sessionId: "id1",
        subscribe: (c) => ((cb = c), () => {}),
        prompt: async () => {
          if (opts.promptError) throw opts.promptError;
          await opts.script?.({
            emit: (e) => cb(e),
            gate: async (tool, input) => !(await handler!({ toolName: tool, toolCallId: "call_1", input }))?.block,
          });
        },
        abort: async () => void (aborted = true),
        dispose: () => {},
      };
      return { session };
    },
  };
  return { sdk, calls, opened, isAborted: () => aborted };
}
const load = (sdk: PiSdk) => async () => sdk;

test("worker：参数交给 SDK，resume 用会话文件路径，用量与历史返回", async () => {
  const f = fakeSdk({ models: { "fake/fake-1": { id: "fake-1" } }, script: async ({ emit }) => toolTurn().forEach(emit) });
  const { io, posted } = fakeIO();
  const out = await runPi(spec({ model: "fake/fake-1", workspace: "/proj" }), io, load(f.sdk));
  assert.equal(out.reason, "completed", out.error);
  assert.equal(out.resume, "/sessions/new.jsonl");
  assert.equal(out.usage.input_tokens, 30);
  assert.equal(f.calls[0].cwd, "/proj");
  assert.equal(f.calls[0].agentDir, "/agent");
  assert.equal(f.calls[0].tools, undefined);
  assert.ok(posted.some((m) => m.type === "emit" && m.event_type === "tool_call"));
});

test("worker：恢复——会话文件存在就打开，不存在就报 resume_lost", async () => {
  const dir = mkdtempSync(join(tmpdir(), "pi-resume-"));
  const file = join(dir, "s.jsonl");
  writeFileSync(file, "{}\n");
  const a = fakeSdk();
  await runPi(spec({ resume: file }), fakeIO().io, load(a.sdk));
  assert.deepEqual(a.opened, [file]);
  assert.equal(a.calls[0].sessionManager.kind, "open");
  // 文件没了：不能悄悄开空会话，什么都不做并报告 resume_lost，由 Go 端补完整历史重跑。
  const b = fakeSdk();
  const { io, posted } = fakeIO();
  const out = await runPi(spec({ resume: join(dir, "没有了.jsonl") }), io, load(b.sdk));
  assert.equal(b.calls.length, 0);
  assert.equal(out.reason, "error");
  assert.equal(out.resume_lost, true);
  assert.equal(out.resume, undefined);
  assert.equal(posted.filter((m) => m.type === "emit").length, 0);
});

test("worker：模型写法、找不到的模型、没有可用模型", async () => {
  const bad = await runPi(spec({ model: "没有斜杠" }), fakeIO().io, load(fakeSdk().sdk));
  assert.equal(bad.reason, "error");
  assert.match(bad.error!, /提供商\/模型ID/);
  const missing = await runPi(spec({ model: "a/b" }), fakeIO().io, load(fakeSdk().sdk));
  assert.match(missing.error!, /没有模型 a\/b/);
  const none = await runPi(spec(), fakeIO().io, load(fakeSdk({ available: 0 }).sdk));
  assert.equal(none.reason, "error");
  assert.match(none.error!, /\/login/);
  const f = fakeSdk({ models: { "a/b": { id: "b" } } });
  await runPi(spec({ model: "a/b" }), fakeIO().io, load(f.sdk));
  assert.deepEqual(f.calls[0].model, { id: "b" });
});

test("worker：只读模式只开读类工具", async () => {
  const f = fakeSdk();
  await runPi(spec({ permission_mode: "readonly" }), fakeIO().io, load(f.sdk));
  assert.deepEqual(f.calls[0].tools, ["read", "grep", "find", "ls"]);
});

test("worker：审批——允许、本会话允许（同一工具不再问）、拒绝", async () => {
  const f = fakeSdk({
    script: async ({ gate }) => {
      assert.equal(await gate("write", { path: "a.txt" }), true); // 允许
      assert.equal(await gate("write", { path: "b.txt" }), true); // 本会话允许
      assert.equal(await gate("write", { path: "c.txt" }), true); // 不再问
      assert.equal(await gate("bash", { command: "rm -rf x" }), false); // 拒绝
      assert.equal(await gate("read", { path: "a.txt" }), true); // 读不问
    },
  });
  const { io, posted, send } = fakeIO();
  const answers: ("allow" | "allow_session" | "deny")[] = ["allow", "allow_session", "deny"];
  const run = runPi(spec(), io, load(f.sdk));
  let answered = 0;
  while (answered < 3) {
    await new Promise((r) => setTimeout(r, 5));
    const asks = posted.filter((m): m is Extract<FromWorker, { type: "ask" }> => m.type === "ask");
    while (answered < asks.length) {
      const a = asks[answered];
      if (answered === 0) assert.deepEqual([a.request.tool, a.request.risk, a.request.call_id], ["write", "write", "call_1"]);
      send({ type: "answer", id: a.id, decision: answers[answered++] });
    }
  }
  const out = await run;
  assert.equal(out.reason, "completed", out.error);
  assert.equal(posted.filter((m) => m.type === "ask").length, 3);
});

test("worker：auto 模式不问", async () => {
  const f = fakeSdk({ script: async ({ gate }) => assert.equal(await gate("bash", { command: "ls" }), true) });
  const { io, posted } = fakeIO();
  const out = await runPi(spec({ permission_mode: "auto" }), io, load(f.sdk));
  assert.equal(out.reason, "completed", out.error);
  assert.equal(posted.filter((m) => m.type === "ask").length, 0);
});

test("worker：取消——中止 pi，等待中的确认按拒绝处理", async () => {
  const f = fakeSdk({
    script: async ({ gate }) => {
      assert.equal(await gate("write", { path: "a" }), false);
    },
  });
  const { io, posted, send } = fakeIO();
  const run = runPi(spec(), io, load(f.sdk));
  while (!posted.some((m) => m.type === "ask")) await new Promise((r) => setTimeout(r, 5));
  send({ type: "cancel" });
  const out = await run;
  assert.equal(out.reason, "cancelled");
  assert.equal(f.isAborted(), true);
});

test("worker：模型错误（消息里的 errorMessage）和 SDK 异常都变成错误", async () => {
  const f = fakeSdk({
    script: async ({ emit }) => {
      emit({ type: "message_start", message: { role: "assistant" } });
      emit({ type: "message_end", message: { role: "assistant", content: [], stopReason: "error", errorMessage: "401 invalid key" } });
    },
  });
  const out = await runPi(spec(), fakeIO().io, load(f.sdk));
  assert.equal(out.reason, "error");
  assert.equal(out.error, "401 invalid key");
  assert.equal(out.resume, "/sessions/new.jsonl"); // 出错也保留会话，下一轮还能接着聊

  const thrown = await runPi(spec(), fakeIO().io, load(fakeSdk({ promptError: new Error("网络断了") }).sdk));
  assert.equal(thrown.reason, "error");
  assert.match(thrown.error!, /网络断了/);

  const noSdk = await runPi(spec(), fakeIO().io, async () => {
    throw new Error("Cannot find module");
  });
  assert.match(noSdk.error!, /无法加载 pi SDK/);
});
