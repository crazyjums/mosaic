import assert from "node:assert/strict";
import { test } from "node:test";

import { handlePreview, PreviewStore } from "../src/preview";

const APP = "mosaic://app";
const TOKEN = "tok-123";
const post = (body: string, token: string | null, scripts = false) =>
  new Request(`mosaic-preview://store/${scripts ? "?scripts=1" : ""}`, { method: "POST", body, headers: token ? { "x-mosaic-token": token } : {} });

test("存入后能取回，响应头把页面关进沙箱", async () => {
  const store = new PreviewStore();
  const r = await handlePreview(post("<h1>hi</h1>", TOKEN), store, APP, TOKEN);
  assert.equal(r.status, 200);
  assert.equal(r.headers.get("access-control-allow-origin"), APP);
  const { url } = (await r.json()) as { url: string };
  const g = await handlePreview(new Request(url), store, APP, TOKEN);
  assert.equal(await g.text(), "<h1>hi</h1>");
  const csp = g.headers.get("content-security-policy")!;
  assert.match(csp, /^sandbox;/);
  assert.match(csp, /default-src 'none'/); // 不允许脚本：禁止联网
  assert.doesNotMatch(csp, /allow-scripts/);
});

test("允许脚本时只是沙箱（没有 allow-same-origin）", async () => {
  const store = new PreviewStore();
  const { url } = (await (await handlePreview(post("<script>1</script>", TOKEN, true), store, APP, TOKEN)).json()) as { url: string };
  const csp = (await handlePreview(new Request(url), store, APP, TOKEN)).headers.get("content-security-policy")!;
  assert.equal(csp, "sandbox allow-scripts");
  assert.doesNotMatch(csp, /allow-same-origin/);
});

test("只接受带正确令牌的存入", async () => {
  const store = new PreviewStore();
  for (const token of [null, "", "wrong", "tok-1234", "tok-12"]) {
    assert.equal((await handlePreview(post("x", token), store, APP, TOKEN)).status, 403, String(token));
  }
  // 后端没有配置令牌（空串）时，空令牌也不能通过。
  assert.equal((await handlePreview(post("x", ""), store, APP, "")).status, 403);
});

test("预检允许带令牌头的 POST", async () => {
  const r = await handlePreview(new Request("mosaic-preview://store/", { method: "OPTIONS" }), new PreviewStore(), APP, TOKEN);
  assert.equal(r.status, 204);
  assert.equal(r.headers.get("access-control-allow-origin"), APP);
  assert.match(r.headers.get("access-control-allow-headers")!, /x-mosaic-token/);
});

test("过大拒绝，旧的被淘汰，未知 id 是 404", async () => {
  const small = new PreviewStore(2, 10);
  assert.equal((await handlePreview(post("x".repeat(11), TOKEN), small, APP, TOKEN)).status, 413);
  const s = new PreviewStore(2, 100);
  const ids: string[] = [];
  for (const body of ["a", "b", "c"]) ids.push(((await (await handlePreview(post(body, TOKEN), s, APP, TOKEN)).json()) as { url: string }).url);
  assert.equal((await handlePreview(new Request(ids[0]), s, APP, TOKEN)).status, 404);
  assert.equal((await handlePreview(new Request(ids[2]), s, APP, TOKEN)).status, 200);
  assert.equal((await handlePreview(new Request("mosaic-preview://view/nope"), s, APP, TOKEN)).status, 404);
});
