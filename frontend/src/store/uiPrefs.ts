// 界面偏好：主题、字号、消息区宽度、思考卡片、发送快捷键、语言（设置 → 外观）。
//
// 与状态栏配置一样，结构完全由前端定义，后端只把它当作一段 JSON 原样存进 settings.json 的 ui 字段。

import { create } from "zustand";
import { backend } from "../bridge";
import { setLanguage, type Lang } from "../i18n";

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
}

export const DEFAULT_PREFS: UIPrefs = {
  version: 1,
  theme: "system",
  fontSize: "medium",
  contentWidth: "medium",
  thinking: "auto",
  sendKey: "enter",
  language: "auto",
};

/** 字号作用在根元素上：Tailwind 的尺寸都是 rem，整个界面会等比缩放。 */
export const FONT_SIZE_PX: Record<UIPrefs["fontSize"], number> = { small: 14, medium: 16, large: 18 };
export const CONTENT_WIDTH: Record<UIPrefs["contentWidth"], string> = { narrow: "40rem", medium: "48rem", wide: "64rem", full: "100%" };

const pick = <T extends string>(v: unknown, allowed: readonly T[], fallback: T): T => (allowed.includes(v as T) ? (v as T) : fallback);

/** 规范化保存的配置：不认识的值回落到默认值，新增的字段使用默认值。 */
export function normalizePrefs(raw: unknown): UIPrefs {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const d = DEFAULT_PREFS;
  return {
    version: 1,
    theme: pick(r.theme, ["system", "light", "dark"], d.theme),
    fontSize: pick(r.fontSize, ["small", "medium", "large"], d.fontSize),
    contentWidth: pick(r.contentWidth, ["narrow", "medium", "wide", "full"], d.contentWidth),
    thinking: pick(r.thinking, ["auto", "expanded", "collapsed"], d.thinking),
    sendKey: pick(r.sendKey, ["enter", "mod_enter"], d.sendKey),
    language: pick(r.language, ["auto", "zh", "en"], d.language),
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
