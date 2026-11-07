import assert from "node:assert/strict";
import { test } from "node:test";

import { Activity, alertText } from "../src/activity";

const ev = (type: string, session_id: string, data: Record<string, unknown> = {}) => ({ type, session_id, data });

test("跟踪正在运行的会话", () => {
  const a = new Activity();
  a.handle(ev("turn_start", "s1"));
  a.handle(ev("turn_start", "s2"));
  a.handle(ev("turn_start", "s1")); // 重复不重复计数
  assert.equal(a.count, 2);
  a.handle(ev("turn_end", "s1", { reason: "completed" }));
  assert.equal(a.count, 1);
});

test("完成、出错要提醒；取消不提醒", () => {
  const a = new Activity();
  a.handle(ev("turn_start", "s1"));
  assert.deepEqual(a.handle(ev("turn_end", "s1", { reason: "completed" })), { kind: "done", sessionId: "s1", failed: false });
  assert.deepEqual(a.handle(ev("turn_end", "s1", { reason: "error" })), { kind: "done", sessionId: "s1", failed: true });
  assert.equal(a.handle(ev("turn_end", "s1", { reason: "cancelled" })), undefined);
  assert.equal(a.count, 0);
});

test("权限请求要提醒，通知里不带对话内容", () => {
  const a = new Activity();
  const alert = a.handle(ev("permission_request", "s9", { tool: "shell", input: { command: "rm -rf /secret" } }));
  assert.deepEqual(alert, { kind: "permission", sessionId: "s9", tool: "shell" });
  const t = alertText(alert!);
  assert.match(t.body, /shell/);
  assert.doesNotMatch(JSON.stringify(t), /secret/);
});

test("乱七八糟的事件被忽略", () => {
  const a = new Activity();
  for (const bad of [null, undefined, 3, "x", {}, { type: "turn_start" }, { type: 5, session_id: "s" }, { type: "text_delta", session_id: "s" }]) {
    assert.equal(a.handle(bad), undefined);
  }
  assert.equal(a.count, 0);
});
