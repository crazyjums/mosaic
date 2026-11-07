import assert from "node:assert/strict";
import { test } from "node:test";

import { ClaudeTranslator, riskOf, type SdkMessage } from "../src/workers/cc-translate";

// 夹具来自对真实 Claude Agent SDK 0.3.319 + claude 2.1.319 的探测（裁剪了无关字段）。

interface Rec {
  id: string;
  type: string;
  data: Record<string, unknown>;
}
function make(): { tr: ClaudeTranslator; events: Rec[] } {
  const events: Rec[] = [];
  const tr = new ClaudeTranslator({ emit: (id, type, data) => events.push({ id, type, data: data as Record<string, unknown> }) }, "claudecode", "claude-sonnet-5-5");
  return { tr, events };
}

const se = (event: SdkMessage["event"]): SdkMessage => ({ type: "stream_event", event, session_id: "sess-1", parent_tool_use_id: null });

const textTurn = (): SdkMessage[] => [
  { type: "system", subtype: "init", session_id: "sess-1" },
  se({ type: "message_start", message: { id: "api-1", model: "claude-sonnet-5-5" } }),
  se({ type: "content_block_start", index: 0, content_block: { type: "text" } }),
  se({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "好" } }),
  se({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "。" } }),
  { type: "assistant", message: { id: "api-1", content: [{ type: "text", text: "好。" }] }, parent_tool_use_id: null },
  se({ type: "content_block_stop", index: 0 }),
  se({ type: "message_delta", delta: { stop_reason: "end_turn" }, usage: { input_tokens: 2, output_tokens: 4, cache_creation_input_tokens: 16227, cache_read_input_tokens: 0 } }),
  se({ type: "message_stop" }),
  { type: "result", subtype: "success", is_error: false, result: "好。", session_id: "sess-1", usage: { input_tokens: 2, output_tokens: 4, cache_creation_input_tokens: 16227, cache_read_input_tokens: 0 } },
];

test("纯文本一轮：事件、历史、用量、会话 ID", () => {
  const { tr, events } = make();
  for (const m of textTurn()) tr.handle(m);
  assert.deepEqual(events.map((e) => e.type), ["message_start", "text_delta", "text_delta", "message_end"]);
  assert.equal(events[0].data.provider, "claudecode");
  assert.equal(events[0].data.model, "claude-sonnet-5-5");
  assert.equal(events[0].data.step, 1);
  assert.ok(events.every((e) => e.id === events[0].id), "同一条助手消息的事件共用一个 ID");
  assert.deepEqual(events[3].data, { stop_reason: "end_turn", usage: { input_tokens: 2, output_tokens: 4, cache_read_tokens: 0, cache_write_tokens: 16227 } });
  assert.equal(tr.result?.ok, true);
  const out = tr.finish("completed");
  assert.equal(out.resume, "sess-1");
  assert.deepEqual(out.usage, { input_tokens: 2, output_tokens: 4, cache_read_tokens: 0, cache_write_tokens: 16227 });
  assert.deepEqual(out.messages, [{ role: "assistant", content: [{ type: "text", text: "好。" }] }]);
});

const toolTurn = (): SdkMessage[] => [
  { type: "system", subtype: "init", session_id: "sess-2" },
  se({ type: "message_start", message: { id: "api-1", model: "m" } }),
  se({ type: "content_block_start", index: 0, content_block: { type: "tool_use", id: "toolu_1", name: "Write" } }),
  se({ type: "content_block_delta", index: 0, delta: { type: "input_json_delta" } }),
  { type: "assistant", message: { id: "api-1", content: [{ type: "tool_use", id: "toolu_1", name: "Write", input: { file_path: "/w/b.txt", content: "hello" } }] }, parent_tool_use_id: null },
  se({ type: "content_block_stop", index: 0 }),
  se({ type: "message_delta", delta: { stop_reason: "tool_use" }, usage: { input_tokens: 2, output_tokens: 74 } }),
  se({ type: "message_stop" }),
  { type: "user", message: { content: [{ tool_use_id: "toolu_1", type: "tool_result", content: "File created successfully", is_error: false }] }, parent_tool_use_id: null },
  se({ type: "message_start", message: { id: "api-2", model: "m" } }),
  se({ type: "content_block_start", index: 0, content_block: { type: "text" } }),
  se({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "已创建。" } }),
  { type: "assistant", message: { id: "api-2", content: [{ type: "text", text: "已创建。" }] }, parent_tool_use_id: null },
  se({ type: "message_delta", delta: { stop_reason: "end_turn" }, usage: { output_tokens: 33 } }),
  se({ type: "message_stop" }),
  { type: "result", subtype: "success", is_error: false, session_id: "sess-2", usage: { input_tokens: 4, output_tokens: 107, cache_read_input_tokens: 32509 } },
];

test("带工具调用的两步：tool_call → tool_result → 第二条消息；历史配对且角色交替", () => {
  const { tr, events } = make();
  for (const m of toolTurn()) tr.handle(m);
  assert.deepEqual(events.map((e) => e.type), ["message_start", "tool_call", "message_end", "tool_result", "message_start", "text_delta", "message_end"]);
  const call = events[1].data;
  assert.deepEqual(call, { call_id: "toolu_1", name: "Write", input: { file_path: "/w/b.txt", content: "hello" }, risk: "write" });
  assert.equal(events[1].id, events[0].id);
  assert.equal(events[3].id, "", "tool_result 不挂在消息上");
  assert.deepEqual(events[3].data, { call_id: "toolu_1", name: "Write", output: "File created successfully", is_error: false, duration_ms: 0 });
  assert.notEqual(events[4].id, events[0].id, "第二条助手消息是新的 ID");
  assert.equal(events[4].data.step, 2);

  const out = tr.finish("completed");
  const msgs = out.messages as { role: string; content: { type: string }[] }[];
  assert.deepEqual(msgs.map((m) => m.role), ["assistant", "user", "assistant"]);
  assert.deepEqual(msgs[0].content[0], { type: "tool_use", tool_call_id: "toolu_1", tool_name: "Write", tool_input: { file_path: "/w/b.txt", content: "hello" } });
  assert.deepEqual(msgs[1].content[0], { type: "tool_result", tool_call_id: "toolu_1", text: "File created successfully", is_error: false });
  assert.deepEqual(out.usage, { input_tokens: 4, output_tokens: 107, cache_read_tokens: 32509, cache_write_tokens: 0 });
});

test("同一条 API 消息的多个块分多次到达：并成一条助手消息，思考块带签名", () => {
  const { tr } = make();
  tr.handle(se({ type: "message_start", message: { id: "a" } }));
  tr.handle({ type: "assistant", message: { id: "a", content: [{ type: "thinking", thinking: "先想想", signature: "sig==" }] }, parent_tool_use_id: null });
  tr.handle({ type: "assistant", message: { id: "a", content: [{ type: "text", text: "好的" }] }, parent_tool_use_id: null });
  tr.handle({ type: "assistant", message: { id: "a", content: [{ type: "text", text: "好的" }] }, parent_tool_use_id: null }); // 重复到达
  tr.handle(se({ type: "message_stop" }));
  const out = tr.finish("completed");
  assert.deepEqual(out.messages, [{ role: "assistant", content: [{ type: "thinking", text: "先想想", signature: "sig==" }, { type: "text", text: "好的" }] }]);
});

test("流式思考增量", () => {
  const { tr, events } = make();
  tr.handle(se({ type: "message_start", message: { id: "a" } }));
  tr.handle(se({ type: "content_block_delta", index: 0, delta: { type: "thinking_delta", thinking: "嗯" } }));
  tr.handle(se({ type: "content_block_delta", index: 0, delta: { type: "signature_delta" } }));
  assert.deepEqual(events.map((e) => e.type), ["message_start", "thinking_delta"]);
});

test("没有流式事件时（未开 partial）也能显示：补全消息和文字", () => {
  const { tr, events } = make();
  tr.handle({ type: "assistant", message: { id: "x", content: [{ type: "text", text: "你好" }, { type: "tool_use", id: "t", name: "Bash", input: { command: "ls" } }] }, parent_tool_use_id: null });
  assert.deepEqual(events.map((e) => e.type), ["message_start", "text_delta", "tool_call", "message_end"]);
  assert.equal(events[3].data.stop_reason, "tool_use");
  assert.equal((events[2].data as { risk: string }).risk, "exec");
});

test("子 agent 里的消息不显示", () => {
  const { tr, events } = make();
  tr.handle({ type: "assistant", message: { id: "s", content: [{ type: "text", text: "子任务" }] }, parent_tool_use_id: "toolu_parent" });
  tr.handle({ ...se({ type: "message_start", message: { id: "s" } }), parent_tool_use_id: "toolu_parent" });
  assert.equal(events.length, 0);
});

test("工具结果是内容块数组、图片、出错", () => {
  const { tr, events } = make();
  tr.handle({ type: "assistant", message: { id: "a", content: [{ type: "tool_use", id: "t1", name: "Read", input: {} }] }, parent_tool_use_id: null });
  tr.handle({ type: "user", message: { content: [{ tool_use_id: "t1", type: "tool_result", content: [{ type: "text", text: "第一行" }, { type: "image" }, { type: "text", text: "第二行" }], is_error: true }] }, parent_tool_use_id: null });
  const r = events.find((e) => e.type === "tool_result")!;
  assert.equal(r.data.output, "第一行\n[图片]\n第二行");
  assert.equal(r.data.is_error, true);
});

test("非 tool_result 的 user 消息（提示词回显）被忽略", () => {
  const { tr, events } = make();
  tr.handle({ type: "user", message: { content: [{ type: "text", text: "你好" }] }, parent_tool_use_id: null });
  assert.equal(events.length, 0);
});

test("取消时：没有结果的工具调用补齐为错误结果；已经输出的内容保留", () => {
  const { tr, events } = make();
  tr.handle(se({ type: "message_start", message: { id: "a" } }));
  tr.handle(se({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "我来" } }));
  tr.handle({ type: "assistant", message: { id: "a", content: [{ type: "text", text: "我来" }, { type: "tool_use", id: "t9", name: "Bash", input: { command: "sleep 100" } }] }, parent_tool_use_id: null });
  const out = tr.finish("cancelled");
  assert.equal(out.reason, "cancelled");
  const last = events[events.length - 1];
  assert.equal(last.type, "tool_result", "界面上的工具卡片也要进入终态");
  assert.equal(last.data.is_error, true);
  const msgs = out.messages as { role: string; content: { type: string; tool_call_id?: string }[] }[];
  assert.deepEqual(msgs.map((m) => m.role), ["assistant", "user"]);
  assert.equal(msgs[1].content[0].tool_call_id, "t9");
  assert.ok(events.some((e) => e.type === "message_end" && e.data.stop_reason === "cancelled"));
});

test("错误结果：error_* 子类型、is_error", () => {
  const { tr } = make();
  tr.handle({ type: "result", subtype: "error_during_execution", is_error: true, errors: ["出错了", "再试一次"], session_id: "s" });
  assert.equal(tr.result?.ok, false);
  assert.equal(tr.result?.error, "出错了；再试一次");
  const t2 = make().tr;
  t2.handle({ type: "result", subtype: "success", is_error: true, result: "Invalid API key · Please run /login", session_id: "s" });
  assert.equal(t2.result?.ok, false);
  assert.match(t2.result!.error, /login/);
});

test("风险级别", () => {
  for (const [t, r] of [["Read", "read"], ["Grep", "read"], ["WebFetch", "read"], ["Write", "write"], ["Edit", "write"], ["Bash", "exec"], ["mcp__x__y", "exec"], ["Task", "exec"]] as const) {
    assert.equal(riskOf(t), r, t);
  }
});

test("被打断时，已经显示但还没有完整块的文字也进历史（重新加载不会丢）", () => {
  const { tr } = make();
  tr.handle(se({ type: "message_start", message: { id: "a" } }));
  tr.handle(se({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "我先看看" } }));
  tr.handle(se({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "目录" } }));
  const out = tr.finish("cancelled");
  assert.deepEqual(out.messages, [{ role: "assistant", content: [{ type: "text", text: "我先看看目录" }] }]);
});

test("被打断：思考已经完整、文字还在流——两者都保留，顺序是思考在先", () => {
  const { tr } = make();
  tr.handle(se({ type: "message_start", message: { id: "a" } }));
  tr.handle(se({ type: "content_block_delta", index: 0, delta: { type: "thinking_delta", thinking: "想" } }));
  tr.handle({ type: "assistant", message: { id: "a", content: [{ type: "thinking", thinking: "想", signature: "s" }] }, parent_tool_use_id: null });
  tr.handle(se({ type: "content_block_delta", index: 1, delta: { type: "text_delta", text: "答" } }));
  const out = tr.finish("cancelled");
  assert.deepEqual(out.messages, [{ role: "assistant", content: [{ type: "thinking", text: "想", signature: "s" }, { type: "text", text: "答" }] }]);
});

test("完整的块已经到了就不重复补", () => {
  const { tr } = make();
  for (const m of textTurn()) tr.handle(m);
  const out = tr.finish("completed");
  assert.deepEqual(out.messages, [{ role: "assistant", content: [{ type: "text", text: "好。" }] }]);
});

test("已经落进历史的消息，之后被打断也不会重复补", () => {
  const { tr } = make();
  tr.handle(se({ type: "message_start", message: { id: "a" } }));
  tr.handle(se({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "我来读" } }));
  tr.handle({ type: "assistant", message: { id: "a", content: [{ type: "text", text: "我来读" }, { type: "tool_use", id: "t1", name: "Read", input: {} }] }, parent_tool_use_id: null });
  tr.handle(se({ type: "message_stop" }));
  tr.handle({ type: "user", message: { content: [{ tool_use_id: "t1", type: "tool_result", content: "内容" }] }, parent_tool_use_id: null });
  const out = tr.finish("cancelled"); // 工具结果之后、下一条消息开始之前被打断
  const msgs = out.messages as { role: string; content: { type: string }[] }[];
  assert.deepEqual(msgs.map((m) => m.role), ["assistant", "user"]);
  assert.equal(msgs[0].content.filter((b) => b.type === "text").length, 1, "文字只出现一次");
});
