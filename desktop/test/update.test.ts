import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { installUpdate, updatesDir, validateUpdatePath, type InstallDeps } from "../src/update";

test("下载目录和 Go 的 os.UserCacheDir()/mosaic/updates 一致", () => {
  // Windows 上 path.join 用反斜杠，统一成正斜杠再比。
  const d = (env: NodeJS.ProcessEnv, platform: NodeJS.Platform, home: string) => updatesDir(env, platform, home).replace(/\\/g, "/");
  assert.equal(d({ LOCALAPPDATA: "C:\\L" }, "win32", "C:\\u"), "C:/L/mosaic/updates");
  assert.equal(d({}, "darwin", "/Users/a"), "/Users/a/Library/Caches/mosaic/updates");
  assert.equal(d({ XDG_CACHE_HOME: "/x" }, "linux", "/home/a"), "/x/mosaic/updates");
  assert.equal(d({}, "linux", "/home/a"), "/home/a/.cache/mosaic/updates");
});

test("只接受下载目录里后端命名的安装包", () => {
  const dir = mkdtempSync(join(tmpdir(), "upd-"));
  const ok = join(dir, ".myllmapp-update-123.AppImage");
  writeFileSync(ok, "x");
  assert.equal(validateUpdatePath(ok, dir, "linux"), ok);
  const other = mkdtempSync(join(tmpdir(), "other-"));
  writeFileSync(join(other, ".myllmapp-update-1.AppImage"), "x");
  writeFileSync(join(dir, "evil.AppImage"), "x");
  writeFileSync(join(dir, ".myllmapp-update-1.sh"), "x");
  for (const bad of [join(other, ".myllmapp-update-1.AppImage"), join(dir, "evil.AppImage"), join(dir, ".myllmapp-update-1.sh"), join(dir, "..", ".myllmapp-update-1.AppImage"), "", null, 3]) {
    assert.throws(() => validateUpdatePath(bad, dir, "linux"), String(bad));
  }
  assert.throws(() => validateUpdatePath(ok, dir, "win32"), /\.exe/);
});

function deps(over: Partial<InstallDeps>): { d: InstallDeps; launched: [string, string[]][]; quit: number[] } {
  const launched: [string, string[]][] = [];
  const quit: number[] = [];
  return { launched, quit, d: { platform: "linux", env: {}, launch: (c, a) => launched.push([c, a]), quit: () => quit.push(1), ...over } };
}

test("Windows：静默运行安装程序并退出", () => {
  const { d, launched, quit } = deps({ platform: "win32" });
  installUpdate("C:\\u\\.myllmapp-update-1.exe", d);
  assert.deepEqual(launched, [["C:\\u\\.myllmapp-update-1.exe", ["/S", "--updated", "--force-run"]]]);
  assert.equal(quit.length, 1);
});

test("Linux：覆盖 AppImage 后重启；不是 AppImage 运行时拒绝且不退出", () => {
  const dir = mkdtempSync(join(tmpdir(), "upd-"));
  const pkg = join(dir, ".myllmapp-update-1.AppImage");
  const target = join(dir, "Mosaic.AppImage");
  writeFileSync(pkg, "new");
  writeFileSync(target, "old");
  const a = deps({ env: { APPIMAGE: target } });
  installUpdate(pkg, a.d);
  assert.equal(readFileSync(target, "utf8"), "new");
  assert.equal(existsSync(`${target}.new`), false);
  assert.equal(a.launched[0][0], "sh");
  assert.equal(a.launched[0][1][2], target);
  assert.equal(a.quit.length, 1);

  const b = deps({ env: {} });
  assert.throws(() => installUpdate(pkg, b.d), /AppImage/);
  assert.equal(b.quit.length, 0);
});

test("macOS 不自动安装", () => {
  const { d, quit } = deps({ platform: "darwin" });
  assert.throws(() => installUpdate("/x", d), /手动/);
  assert.equal(quit.length, 0);
});
