import { describe, expect, it } from "vitest";
import { BUILTIN_SKINS, exportSkin, parseSkin, SKIN_VARS, skinCSS } from "./skins";
import { normalizePrefs } from "./uiPrefs";
import indexCSS from "../index.css?raw";

// WCAG 相对亮度与对比度，用来检查内置皮肤的可读性。
function luminance(hex: string): number {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
function contrast(a: string, b: string): number {
  const [x, y] = [luminance(a), luminance(b)].sort((m, n) => n - m);
  return (x + 0.05) / (y + 0.05);
}

describe("skins", () => {
  it("built-in skins are complete and readable", () => {
    for (const s of BUILTIN_SKINS) {
      for (const mode of ["dark", "light"] as const) {
        const p = s[mode];
        for (const k of SKIN_VARS) expect(p[k], `${s.id}.${mode}.${k}`).toMatch(/^#[0-9a-f]{6}$/);
        // 正文 ≥ 7（AAA），次要文字 ≥ 4.5（AA），强调色按钮上的文字 ≥ 3（大号 / 粗体文字的 AA）
        expect(contrast(p.fg, p.bg), `${s.id}.${mode} fg/bg`).toBeGreaterThanOrEqual(7);
        expect(contrast(p.muted, p.bg), `${s.id}.${mode} muted/bg`).toBeGreaterThanOrEqual(4.5);
        expect(contrast(p["on-accent"], p.accent), `${s.id}.${mode} on-accent/accent`).toBeGreaterThanOrEqual(3);
        // 状态色和强调色会用作文字和图标颜色，至少 3:1
        for (const k of ["accent", "think", "ok", "warn", "danger"] as const) {
          expect(contrast(p[k], p.bg), `${s.id}.${mode} ${k}/bg`).toBeGreaterThanOrEqual(3);
        }
      }
    }
  });

  it("the default skin matches index.css", () => {
    // 选中"默认"时不注入规则、直接用 index.css，所以两边的颜色必须一致（编辑器以它为起点复制）。
    const block = (selector: string) => indexCSS.slice(indexCSS.indexOf(selector), indexCSS.indexOf("}", indexCSS.indexOf(selector)));
    const read = (css: string, k: string) => css.match(new RegExp(`--${k}:\\s*(#[0-9a-f]{6})`))?.[1];
    const dark = block(":root {"), light = block(':root[data-theme="light"]');
    for (const k of SKIN_VARS) {
      expect(read(dark, k), `dark ${k}`).toBe(BUILTIN_SKINS[0].dark[k]);
      expect(read(light, k), `light ${k}`).toBe(BUILTIN_SKINS[0].light[k]);
    }
  });

  it("parses and round-trips an exported skin", () => {
    const s = BUILTIN_SKINS[1];
    const back = parseSkin(JSON.parse(exportSkin(s)));
    expect(typeof back).not.toBe("string");
    if (typeof back !== "string") {
      expect(back.dark).toEqual(s.dark);
      expect(back.id).toMatch(/^custom-/);
    }
  });

  it("rejects anything that is not #rrggbb (no CSS injection)", () => {
    expect(parseSkin({ name: "x", dark: { bg: "red;} body{display:none" }, light: {} })).toMatch(/bg/);
    expect(parseSkin({ name: "x", dark: { accent: "#fff" }, light: {} })).toMatch(/accent/);
    expect(parseSkin({ dark: {}, light: {} })).toMatch(/name/);
    expect(parseSkin("nope")).toBeTypeOf("string");
  });

  it("fills missing colors from the default skin", () => {
    const s = parseSkin({ name: "partial", dark: { accent: "#FF8800" }, light: {} });
    if (typeof s === "string") throw new Error(s);
    expect(s.dark.accent).toBe("#ff8800");
    expect(s.dark.bg).toBe(BUILTIN_SKINS[0].dark.bg);
  });

  it("never emits an invalid color into the CSS (live preview applies unvalidated drafts)", () => {
    const evil = { ...BUILTIN_SKINS[1], dark: { ...BUILTIN_SKINS[1].dark, accent: "red;}body{display:none" } };
    const css = skinCSS(evil);
    expect(css).not.toContain("display:none");
    expect(css).toContain(`--accent: ${BUILTIN_SKINS[0].dark.accent};`);
  });

  it("generates rules for dark, system-light and forced-light", () => {
    const css = skinCSS(BUILTIN_SKINS[1]);
    expect(css).toContain(":root {");
    expect(css).toContain('@media (prefers-color-scheme: light)');
    expect(css).toContain(':root:not([data-theme="dark"])');
    expect(css).toContain(':root[data-theme="light"]');
    expect(css).toContain("--on-accent:");
  });

  it("normalizePrefs keeps valid custom skins and falls back for unknown skin ids", () => {
    const good = { id: "custom-abc", name: "Mine", dark: {}, light: {} };
    const bad = { id: "custom-bad", name: "Bad", dark: { bg: "url(x)" }, light: {} };
    const p = normalizePrefs({ skin: "custom-abc", customSkins: [good, bad, good] });
    expect(p.customSkins.map((s) => s.id)).toEqual(["custom-abc"]);
    expect(p.skin).toBe("custom-abc");
    expect(normalizePrefs({ skin: "custom-bad", customSkins: [bad] }).skin).toBe("default");
    expect(normalizePrefs({ skin: "ocean" }).skin).toBe("ocean");
  });
});
