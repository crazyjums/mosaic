import assert from "node:assert/strict";
import { test } from "node:test";

// host.ts 会 import electron；在普通 Node 里 electron 包只导出可执行文件路径，所以只测不依赖 electron 对象的纯函数。
import { isWebURL, toElectronFilters } from "../src/host";

test("只放行 http / https 链接", () => {
  for (const ok of ["https://example.com/a?b=1", "http://localhost:5173/"]) assert.equal(isWebURL(ok), true, ok);
  for (const bad of ["file:///etc/passwd", "javascript:alert(1)", "mosaic://app/", "ms-settings:", "vbscript:x", "", "not a url", null, undefined, 5]) {
    assert.equal(isWebURL(bad as unknown), false, String(bad));
  }
});

test("Wails 风格的过滤器转成 Electron 的扩展名数组", () => {
  assert.deepEqual(toElectronFilters([{ display_name: "CSV (*.csv)", pattern: "*.csv" }, { display_name: "图片", pattern: "*.png; *.jpg" }, { display_name: "全部", pattern: "*.*" }, { display_name: "任意", pattern: "*" }]), [
    { name: "CSV (*.csv)", extensions: ["csv"] },
    { name: "图片", extensions: ["png", "jpg"] },
    { name: "全部", extensions: ["*"] },
    { name: "任意", extensions: ["*"] },
  ]);
  assert.deepEqual(toElectronFilters(undefined), []);
});
