import assert from "node:assert/strict";
import { test } from "node:test";

import { auxWindowSpec } from "../src/windows";

test("git 窗口", () => {
  const s = auxWindowSpec("git", undefined);
  assert.equal(s.key, "git");
  assert.equal(s.query, "?window=git");
});

test("产物窗口：参数编码进地址，同一个文件同一个 key", () => {
  const params = { path: "/work/a b/报告 & 图.html", workspace: "/work/a b", kind: "html" };
  const s = auxWindowSpec("artifact", params);
  // key 是规范化后的路径，具体写法随平台（Windows 上是小写反斜杠），所以只断言前缀和“等价路径同 key”。
  assert.ok(s.key.startsWith("artifact:"));
  assert.equal(s.key, auxWindowSpec("artifact", { ...params, path: "/work/a b/./报告 & 图.html" }).key);
  assert.equal(s.title, "Mosaic — 报告 & 图.html");
  const q = new URLSearchParams(s.query);
  assert.equal(q.get("window"), "artifact");
  assert.equal(q.get("path"), params.path);
  assert.equal(q.get("workspace"), params.workspace);
  assert.equal(q.get("kind"), "html");
  assert.equal(s.query.includes(" "), false); // 空格和 & 都被编码，不会截断参数
});

test("等价的路径是同一个窗口", () => {
  const mk = (path: string) => auxWindowSpec("artifact", { path, workspace: "/w", kind: "markdown" }).key;
  assert.equal(mk("/w/a.md"), mk("/w/sub/../a.md"));
  assert.equal(mk("/w/a.md"), mk("/w/./a.md"));
  assert.equal(mk("/w//a.md"), mk("/w/a.md"));
  assert.notEqual(mk("/w/a.md"), mk("/w/b.md"));
});

test("拒绝不在白名单里的类型和不合法的参数", () => {
  for (const kind of ["shell", "", null, undefined, 5, "GIT", "artifact "]) assert.throws(() => auxWindowSpec(kind, {}), /不支持/, String(kind));
  const ok = { path: "/w/a.html", workspace: "/w", kind: "html" };
  for (const bad of [null, {}, { ...ok, path: "" }, { ...ok, path: 3 }, { ...ok, workspace: undefined }, { ...ok, kind: "exe" }, { ...ok, path: "a\0b" }, { ...ok, path: "x".repeat(5000) }]) {
    assert.throws(() => auxWindowSpec("artifact", bad), /不合法/, JSON.stringify(bad)?.slice(0, 60));
  }
});
