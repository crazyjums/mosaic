import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";

import { findPiEntry } from "../src/workers/pi-locate";

const rel = join("node_modules", "@earendil-works", "pi-coding-agent", "dist", "bundle", "index.js");

test("打包版优先：resources/pi/index.js", () => {
  const res = join("/app", "resources");
  const got = findPiEntry("/app/resources/app.asar.unpacked/dist/src/workers", res, (p) => p === join(res, "pi", "index.js"));
  assert.equal(got, join(res, "pi", "index.js"));
});

test("开发时往上找 node_modules", () => {
  const want = join("/repo/desktop", rel);
  assert.equal(findPiEntry("/repo/desktop/dist/src/workers", undefined, (p) => p === want), want);
});

test("哪里都没有就返回 undefined", () => {
  assert.equal(findPiEntry("/a/b/c", "/res", () => false), undefined);
});
