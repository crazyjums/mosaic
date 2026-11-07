import assert from "node:assert/strict";
import { test } from "node:test";
import { Peer, RemoteError } from "../src/rpc";

function pair() {
  const toB: string[] = [];
  const a = new Peer((l) => toB.push(l), async (m, p) => (m === "echo" ? p : Promise.reject(new Error("没有 " + m))));
  const sent: string[] = [];
  const b = new Peer((l) => sent.push(l), () => null);
  return { a, b, toB, sent };
}

test("只按 LF 切分：JSON 字符串里的 U+2028 不会把消息切开，任意切分的片段也能拼回来", () => {
  const got: unknown[] = [];
  const p = new Peer(() => {}, (_m, params) => void got.push(params));
  const line = JSON.stringify({ jsonrpc: "2.0", method: "event", params: { text: "a b c" } }) + "\n";
  for (let i = 0; i < line.length; i += 7) p.feed(line.slice(i, i + 7)); // 7 个字符一片
  return new Promise<void>((resolve) =>
    setImmediate(() => {
      assert.deepEqual(got, [{ text: "a b c" }]);
      resolve();
    }),
  );
});

test("请求按 id 配对回应；错误带着文本和错误码", async () => {
  const { b, sent } = pair();
  const r1 = b.call("A", [1]);
  const r2 = b.call("B", []);
  const reqs = sent.map((l) => JSON.parse(l));
  assert.deepEqual(reqs.map((r) => r.method), ["A", "B"]);
  // 乱序回应也能配对。
  b.feed(JSON.stringify({ jsonrpc: "2.0", id: reqs[1].id, error: { code: -32000, message: "出错了" } }) + "\n");
  b.feed(JSON.stringify({ jsonrpc: "2.0", id: reqs[0].id, result: { ok: true } }) + "\n");
  assert.deepEqual(await r1, { ok: true });
  await assert.rejects(r2, (e: unknown) => e instanceof RemoteError && e.message === "出错了" && e.code === -32000);
});

test("对方的请求：有 id 就回应（成功或错误），通知不回应", async () => {
  const { a, toB } = pair();
  a.feed(JSON.stringify({ jsonrpc: "2.0", id: "s1", method: "echo", params: [1, 2] }) + "\n");
  a.feed(JSON.stringify({ jsonrpc: "2.0", id: "s2", method: "nope" }) + "\n");
  a.feed(JSON.stringify({ jsonrpc: "2.0", method: "echo", params: 1 }) + "\n"); // 通知
  await new Promise((r) => setImmediate(r));
  const out = toB.map((l) => JSON.parse(l));
  assert.equal(out.length, 2);
  assert.deepEqual(out.find((o) => o.id === "s1").result, [1, 2]);
  assert.equal(out.find((o) => o.id === "s2").error.message, "没有 nope");
});

test("连接断开时等待中的调用立刻失败，之后再调用也失败", async () => {
  const { b } = pair();
  const r = b.call("A");
  b.close("后端退出了");
  await assert.rejects(r, /后端退出了/);
  await assert.rejects(b.call("B"), /已断开/);
});

test("不是 JSON 的行被忽略，不影响后面的消息", async () => {
  const { b, sent } = pair();
  const r = b.call("A");
  b.feed("go: some stray log line\n");
  b.feed(JSON.stringify({ jsonrpc: "2.0", id: JSON.parse(sent[0]).id, result: 7 }) + "\n");
  assert.equal(await r, 7);
});
