// 界面偏好：主题、皮肤、字号、消息区宽度、思考卡片、发送快捷键、语言（设置 → 外观）。
//
// 与状态栏配置一样，结构完全由前端定义，后端只把它当作一段 JSON 原样存进 settings.json 的 ui 字段。

import { create } from "zustand";
import { backend } from "../bridge";
import { setLanguage, type Lang } from "../i18n";
import { applySkin, BUILTIN_SKINS, DEFAULT_SKIN_ID, findSkin, parseSkin, type Skin } from "./skins";

export interface UIPrefs {
  version: 1;
  /** system：跟随系统 */
  theme: "system" | "light" | "dark";
  fontSize: "small" | "medium" | "large";
  contentWidth: "narrow" | "medium" | "wide" | "full";
  /** 思考卡片：auto = 流式输出时展开、结束后折叠 */
  thinking: "auto" | "expanded" | "collapsed";
  /** 发送消息的快捷键；另一种组合用来换行 */
  sendKey: "enter" | "mod_enter";
  /** auto：按系统语言 */
  language: "auto" | Lang;
  /** 当前皮肤的 id（内置皮肤或自定义皮肤），见 store/skins.ts */
  skin: string;
  /** 用户自定义的皮肤 */
  customSkins: Skin[];
  /** 是否显示右侧的上下文明细面板（窗口足够宽时） */
  contextPanel: boolean;
  /** 左侧会话栏是否收起（收起后只剩一列图标） */
  sidebarCollapsed: boolean;
}

export const DEFAULT_PREFS: UIPrefs = {
  version: 1,
  theme: "system",
  fontSize: "medium",
  contentWidth: "medium",
  thinking: "auto",
  sendKey: "enter",
  language: "auto",
  skin: DEFAULT_SKIN_ID,
  customSkins: [],
  contextPanel: true,
  sidebarCollapsed: false,
};

/** 字号作用在根元素上：Tailwind 的尺寸都是 rem，整个界面会等比缩放。 */
export const FONT_SIZE_PX: Record<UIPrefs["fontSize"], number> = { small: 14, medium: 16, large: 18 };
export const CONTENT_WIDTH: Record<UIPrefs["contentWidth"], string> = { narrow: "40rem", medium: "48rem", wide: "64rem", full: "100%" };

const pick = <T extends string>(v: unknown, allowed: readonly T[], fallback: T): T => (allowed.includes(v as T) ? (v as T) : fallback);

/** 规范化保存的配置：不认识的值回落到默认值，新增的字段使用默认值。 */
export function normalizePrefs(raw: unknown): UIPrefs {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const d = DEFAULT_PREFS;
  // 自定义皮肤逐个校验，坏掉的（例如手改 settings.json 写错了颜色）直接丢弃，不影响其他偏好。
  const customSkins: Skin[] = [];
  for (const raw of Array.isArray(r.customSkins) ? r.customSkins : []) {
    const sk = parseSkin(raw);
    if (typeof sk !== "string" && !customSkins.some((c) => c.id === sk.id)) customSkins.push(sk);
  }
  const ids = [...BUILTIN_SKINS, ...customSkins].map((s) => s.id);
  return {
    version: 1,
    theme: pick(r.theme, ["system", "light", "dark"], d.theme),
    fontSize: pick(r.fontSize, ["small", "medium", "large"], d.fontSize),
    contentWidth: pick(r.contentWidth, ["narrow", "medium", "wide", "full"], d.contentWidth),
    thinking: pick(r.thinking, ["auto", "expanded", "collapsed"], d.thinking),
    sendKey: pick(r.sendKey, ["enter", "mod_enter"], d.sendKey),
    language: pick(r.language, ["auto", "zh", "en"], d.language),
    skin: pick(r.skin, ids, d.skin),
    customSkins,
    contextPanel: typeof r.contextPanel === "boolean" ? r.contextPanel : d.contextPanel,
    sidebarCollapsed: typeof r.sidebarCollapsed === "boolean" ? r.sidebarCollapsed : d.sidebarCollapsed,
  };
}

export function resolveLanguage(p: UIPrefs["language"], browser = typeof navigator !== "undefined" ? navigator.language : "zh"): Lang {
  if (p !== "auto") return p;
  return browser.toLowerCase().startsWith("zh") ? "zh" : "en";
}

/** 把偏好应用到页面：主题和宽度通过根元素上的属性 / CSS 变量生效（见 index.css）。 */
export function applyPrefs(p: UIPrefs) {
  const root = document.documentElement;
  if (p.theme === "system") delete root.dataset.theme;
  else root.dataset.theme = p.theme;
  root.style.fontSize = `${FONT_SIZE_PX[p.fontSize]}px`;
  root.style.setProperty("--content-width", CONTENT_WIDTH[p.contentWidth]);
  applySkin(findSkin(p.skin, p.customSkins));
  const lang = resolveLanguage(p.language);
  root.lang = lang === "zh" ? "zh-CN" : "en";
  setLanguage(lang);
}

interface UIPrefsStore {
  prefs: UIPrefs;
  /** 实际使用的语言（language 为 auto 时按系统语言决定），组件用它触发重新渲染 */
  lang: Lang;
  load(): Promise<void>;
  /** 修改并立即保存 */
  update(patch: Partial<UIPrefs>): Promise<void>;
}

// 在第一次渲染之前就确定语言：t() 读的模块变量和 store 里的 lang 必须一致，
// 否则偏好加载完、lang 没变时不会触发重新渲染，先渲染的组件会停留在另一种语言。
const initialLang = resolveLanguage(DEFAULT_PREFS.language);
setLanguage(initialLang);

export const useUIPrefs = create<UIPrefsStore>((set, get) => ({
  prefs: DEFAULT_PREFS,
  lang: initialLang,

  async load() {
    let prefs = DEFAULT_PREFS;
    try {
      prefs = normalizePrefs(await backend().getUIPrefs());
    } catch {
      // 读不到就用默认偏好。
    }
    applyPrefs(prefs);
    set({ prefs, lang: resolveLanguage(prefs.language) });
  },

  async update(patch) {
    const prefs = { ...get().prefs, ...patch };
    applyPrefs(prefs);
    set({ prefs, lang: resolveLanguage(prefs.language) }); // 先更新界面，再保存
    await backend().saveUIPrefs(prefs);
  },
}));
