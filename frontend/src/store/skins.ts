// 皮肤（设置 → 外观 → 皮肤）：一套配色，分深色和浅色两份。
//
// 界面上所有颜色都来自 index.css 里的 CSS 变量（--bg、--accent……），组件里没有写死的颜色，
// 所以换皮肤只需要换一组变量：applySkin 生成与 index.css 结构相同的规则，放进 <style id="skin">。
// "跟随系统 / 浅色 / 深色"仍由主题设置决定，皮肤只决定"深色是什么样、浅色是什么样"。
//
// 自定义皮肤保存在界面偏好里（settings.json 的 ui 字段），可以导出成 JSON 分享、再导入。
// 颜色会被拼进 CSS 文本，所以导入时严格校验：只接受 #rrggbb，杜绝借皮肤注入任意 CSS。

export const SKIN_VARS = ["bg", "panel", "panel-2", "border", "fg", "muted", "accent", "on-accent", "think", "ok", "warn", "danger"] as const;
export type SkinVar = (typeof SKIN_VARS)[number];
export type Palette = Record<SkinVar, string>;

export interface Skin {
  id: string;
  name: string;
  dark: Palette;
  light: Palette;
}

/** 编辑器里每个颜色的说明（中文原文，渲染时翻译）。 */
export const VAR_LABELS: Record<SkinVar, string> = {
  bg: "背景",
  panel: "面板",
  "panel-2": "次级面板",
  border: "边框",
  fg: "文字",
  muted: "次要文字",
  accent: "强调色",
  "on-accent": "强调色上的文字",
  think: "思考过程",
  ok: "成功",
  warn: "警告",
  danger: "危险",
};

const p = (
  bg: string, panel: string, panel2: string, border: string, fg: string, muted: string,
  accent: string, onAccent: string, think: string, ok: string, warn: string, danger: string,
): Palette => ({ bg, panel, "panel-2": panel2, border, fg, muted, accent, "on-accent": onAccent, think, ok, warn, danger });

/** 内置皮肤。第一个是默认皮肤，颜色与 index.css 一致（选中它时不注入任何规则）。 */
export const BUILTIN_SKINS: Skin[] = [
  {
    id: "default",
    name: "默认",
    dark: p("#0f1115", "#161920", "#1d212a", "#2a2f3a", "#e6e8ec", "#8b93a3", "#7c9cff", "#0b1330", "#a78bfa", "#4ade80", "#fbbf24", "#f87171"),
    light: p("#f7f8fa", "#ffffff", "#f1f3f6", "#e1e4ea", "#1b1f27", "#697186", "#3b5bdb", "#ffffff", "#7c3aed", "#16a34a", "#b45309", "#dc2626"),
  },
  {
    id: "ocean",
    name: "海洋",
    dark: p("#0b1320", "#101b2d", "#16243a", "#22344f", "#e2ecf7", "#8aa0ba", "#38bdf8", "#04263a", "#818cf8", "#34d399", "#fbbf24", "#f87171"),
    light: p("#f2f7fb", "#ffffff", "#e8f1f8", "#d3e2ee", "#0f2133", "#5b7189", "#0369a1", "#ffffff", "#6366f1", "#059669", "#b45309", "#dc2626"),
  },
  {
    id: "forest",
    name: "森林",
    dark: p("#0e1411", "#141c17", "#1b261f", "#2a3a30", "#e3ece5", "#8fa396", "#34d399", "#052e1c", "#c084fc", "#a3e635", "#facc15", "#f87171"),
    light: p("#f4f8f4", "#ffffff", "#eaf2ec", "#d5e3d8", "#14231a", "#5e7465", "#047857", "#ffffff", "#7c3aed", "#4d7c0f", "#a16207", "#dc2626"),
  },
  {
    id: "sunset",
    name: "暖阳",
    dark: p("#16110d", "#1e1712", "#271e17", "#3a2d22", "#f2e8df", "#a8968a", "#fb923c", "#2b1303", "#f472b6", "#4ade80", "#facc15", "#f87171"),
    light: p("#fbf7f2", "#ffffff", "#f5ede3", "#eadccb", "#2a1d12", "#7d6a5a", "#c2410c", "#ffffff", "#db2777", "#15803d", "#a16207", "#dc2626"),
  },
  {
    id: "rose",
    name: "玫瑰",
    dark: p("#170f14", "#1f141b", "#291b24", "#3d2836", "#f3e6ee", "#ab92a2", "#f472b6", "#2e0a1c", "#c4b5fd", "#4ade80", "#fbbf24", "#fb7185"),
    light: p("#fcf5f8", "#ffffff", "#f8e9f0", "#efd5e1", "#2d1622", "#85647a", "#be185d", "#ffffff", "#7c3aed", "#15803d", "#b45309", "#e11d48"),
  },
  {
    id: "nord",
    name: "Nord",
    dark: p("#2e3440", "#3b4252", "#434c5e", "#4c566a", "#eceff4", "#a3acbd", "#88c0d0", "#1f252e", "#b48ead", "#a3be8c", "#ebcb8b", "#bf616a"),
    light: p("#eceff4", "#f8f9fb", "#e5e9f0", "#d8dee9", "#2e3440", "#5e6a80", "#5e81ac", "#ffffff", "#8f6a89", "#5f7f45", "#9a6b17", "#bf616a"),
  },
  {
    id: "contrast",
    name: "高对比",
    dark: p("#000000", "#0a0a0a", "#161616", "#5c5c5c", "#ffffff", "#c8c8c8", "#ffd60a", "#000000", "#c49cff", "#3ef08b", "#ffb020", "#ff6b6b"),
    light: p("#ffffff", "#ffffff", "#f0f0f0", "#767676", "#000000", "#3d3d3d", "#0040c1", "#ffffff", "#6a1fbf", "#00702e", "#8a4b00", "#b00020"),
  },
];

export const DEFAULT_SKIN_ID = "default";

const HEX = /^#[0-9a-fA-F]{6}$/;

/** 校验一份调色板：12 个颜色都必须是 #rrggbb。缺少的颜色用 fallback 补上（兼容旧版本导出的皮肤）。 */
function parsePalette(raw: unknown, fallback: Palette): Palette | string {
  if (!raw || typeof raw !== "object") return "缺少颜色定义";
  const r = raw as Record<string, unknown>;
  const out = { ...fallback };
  for (const k of SKIN_VARS) {
    const v = r[k];
    if (v === undefined) continue;
    if (typeof v !== "string" || !HEX.test(v)) return `颜色 ${k} 的值 ${JSON.stringify(v)} 不是 #rrggbb 格式`;
    out[k] = v.toLowerCase();
  }
  return out;
}

/**
 * 解析一个皮肤（来自保存的偏好或用户导入的 JSON）。返回皮肤，或说明哪里不对的错误文字。
 * id 由调用方决定是否保留：导入时总是生成新的 id，避免和已有皮肤冲突。
 */
export function parseSkin(raw: unknown): Skin | string {
  if (!raw || typeof raw !== "object") return "不是一个 JSON 对象";
  const r = raw as Record<string, unknown>;
  const name = typeof r.name === "string" ? r.name.trim().slice(0, 40) : "";
  if (!name) return "缺少皮肤名称（name）";
  const base = BUILTIN_SKINS[0];
  const dark = parsePalette(r.dark, base.dark);
  if (typeof dark === "string") return "dark：" + dark;
  const light = parsePalette(r.light, base.light);
  if (typeof light === "string") return "light：" + light;
  const id = typeof r.id === "string" && /^custom-[\w-]{1,40}$/.test(r.id) ? r.id : newSkinId();
  return { id, name, dark, light };
}

export function newSkinId(): string {
  return "custom-" + Math.random().toString(36).slice(2, 10);
}

/** 导出给用户分享的 JSON（不含 id：导入方会生成自己的）。 */
export function exportSkin(s: Skin): string {
  return JSON.stringify({ name: s.name, dark: s.dark, light: s.light }, null, 2);
}

export function findSkin(id: string, custom: Skin[]): Skin {
  return BUILTIN_SKINS.find((s) => s.id === id) ?? custom.find((s) => s.id === id) ?? BUILTIN_SKINS[0];
}

// 生成 CSS 时再校验一次每个颜色：编辑器的实时预览会应用还没校验的草稿（用户正在输入），
// 不合法的值用默认皮肤的颜色代替，保证任何内容都不可能拼出额外的 CSS 规则。
const decls = (pal: Palette, fallback: Palette, indent: string) =>
  SKIN_VARS.map((k) => `${indent}--${k}: ${HEX.test(pal[k] ?? "") ? pal[k] : fallback[k]};`).join("\n");

/** 生成与 index.css 结构相同的规则：默认深色；系统浅色时用浅色（除非固定为深色）；固定浅色时用浅色。 */
export function skinCSS(s: Skin): string {
  const base = BUILTIN_SKINS[0];
  return [
    `:root {\n  color-scheme: dark;\n${decls(s.dark, base.dark, "  ")}\n}`,
    `@media (prefers-color-scheme: light) {\n  :root:not([data-theme="dark"]) {\n    color-scheme: light;\n${decls(s.light, base.light, "    ")}\n  }\n}`,
    `:root[data-theme="light"] {\n  color-scheme: light;\n${decls(s.light, base.light, "  ")}\n}`,
  ].join("\n");
}

/** 应用皮肤。默认皮肤不注入规则，直接用 index.css。 */
export function applySkin(s: Skin) {
  if (typeof document === "undefined") return;
  let el = document.getElementById("skin") as HTMLStyleElement | null;
  if (s.id === DEFAULT_SKIN_ID) {
    el?.remove();
    return;
  }
  if (!el) {
    el = document.createElement("style");
    el.id = "skin";
    document.head.appendChild(el); // 放在最后：与 index.css 的规则优先级相同，后出现的生效
  }
  el.textContent = skinCSS(s);
}
