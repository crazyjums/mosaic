import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { defaults, DEFAULT_SHORTCUT, normalize, SettingsFile, validShortcut } from "../src/settings";

test("快捷键格式：要有修饰键和按键，只允许字母数字和加号", () => {
  for (const ok of ["CommandOrControl+Shift+M", "Alt+Space", "Ctrl+Alt+1", "Super+F12"]) assert.equal(validShortcut(ok), true, ok);
  for (const bad of ["", "M", "Shift", "Shift+Ctrl", "Ctrl+", "+M", "Ctrl++M", "Ctrl+Shift+M; rm", "Ctrl + M", "Foo+M", 5, null, "Ctrl+" + "A".repeat(50)]) {
    assert.equal(validShortcut(bad), false, String(bad));
  }
});

test("默认值，环境变量只影响快捷键默认值", () => {
  assert.deepEqual(defaults({}), { closeBehavior: "tray-when-running", notifications: true, quickAsk: { enabled: true, shortcut: DEFAULT_SHORTCUT } });
  assert.deepEqual(defaults({ MOSAIC_QUICK_SHORTCUT: "" }).quickAsk, { enabled: false, shortcut: DEFAULT_SHORTCUT });
  assert.deepEqual(defaults({ MOSAIC_QUICK_SHORTCUT: "Alt+Space" }).quickAsk, { enabled: true, shortcut: "Alt+Space" });
});

test("规整：不合法的字段回退，合法的保留", () => {
  const base = defaults({});
  const n = normalize({ closeBehavior: "explode", notifications: "yes", quickAsk: { enabled: false, shortcut: "nope" } }, base);
  assert.deepEqual(n, { ...base, quickAsk: { enabled: false, shortcut: DEFAULT_SHORTCUT } });
  assert.equal(normalize({ closeBehavior: "quit" }, base).closeBehavior, "quit");
  assert.deepEqual(normalize(null, base), base);
  assert.deepEqual(normalize("x", base), base);
});

test("存取往返；文件不存在或损坏回退默认值", () => {
  const dir = mkdtempSync(join(tmpdir(), "shell-settings-"));
  const f = new SettingsFile(join(dir, "sub", "s.json"), {});
  assert.deepEqual(f.load(), defaults({}));
  const s = { closeBehavior: "quit" as const, notifications: false, quickAsk: { enabled: true, shortcut: "Alt+Space" } };
  f.save(s);
  assert.deepEqual(f.load(), s);
  assert.equal(JSON.parse(readFileSync(join(dir, "sub", "s.json"), "utf8")).closeBehavior, "quit");
  writeFileSync(join(dir, "sub", "s.json"), "{ not json");
  assert.deepEqual(f.load(), defaults({}));
});
