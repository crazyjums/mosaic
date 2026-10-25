import type { ReactNode } from "react";
import { t } from "../i18n";
import { MOD_KEY } from "../store/keys";
import { DEFAULT_PREFS, resolveLanguage, useUIPrefs, type UIPrefs } from "../store/uiPrefs";
import SkinSettings from "./SkinSettings";

// 设置 → 外观：主题、皮肤、字体大小、消息区宽度、思考卡片、发送快捷键、界面语言。
// 都是纯界面偏好，修改立即生效并保存（settings.json 的 ui 字段）。

export default function AppearanceSettings() {
  const { prefs, update } = useUIPrefs();
  const set = <K extends keyof UIPrefs>(k: K) => (v: UIPrefs[K]) => void update({ [k]: v } as Partial<UIPrefs>);
  const systemLang = resolveLanguage("auto") === "zh" ? "中文" : "English";

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex-1 space-y-6 overflow-y-auto px-6 py-5">
        <Row label={t("主题")}>
          <Segmented
            value={prefs.theme}
            onChange={set("theme")}
            options={[
              ["system", t("跟随系统")],
              ["light", t("浅色")],
              ["dark", t("深色")],
            ]}
          />
        </Row>

        <Row label={t("皮肤")} hint={t("皮肤决定深色和浅色各是什么配色；上面的主题决定显示深色还是浅色。")}>
          <SkinSettings />
        </Row>

        <Row label={t("字体大小")} hint={t("整个界面按比例缩放。")}>
          <Segmented
            value={prefs.fontSize}
            onChange={set("fontSize")}
            options={[
              ["small", t("小")],
              ["medium", t("中")],
              ["large", t("大")],
            ]}
          />
        </Row>

        <Row label={t("消息区宽度")}>
          <Segmented
            value={prefs.contentWidth}
            onChange={set("contentWidth")}
            options={[
              ["narrow", t("窄")],
              ["medium", t("中")],
              ["wide", t("宽")],
              ["full", t("铺满")],
            ]}
          />
        </Row>

        <Row label={t("思考过程默认")}>
          <Segmented
            value={prefs.thinking}
            onChange={set("thinking")}
            options={[
              ["auto", t("自动（输出时展开，结束后折叠）")],
              ["expanded", t("总是展开")],
              ["collapsed", t("总是折叠")],
            ]}
          />
        </Row>

        <Row label={t("发送快捷键")} hint={t("适合经常输入多行内容（代码、列表）的情况。")}>
          <Segmented
            value={prefs.sendKey}
            onChange={set("sendKey")}
            options={[
              ["enter", t("Enter 发送，Shift+Enter 换行")],
              ["mod_enter", t("{key}+Enter 发送，Enter 换行", { key: MOD_KEY })],
            ]}
          />
        </Row>

        <Row label={t("界面语言")}>
          {/* 语言名称不翻译：用户要能在看不懂当前语言时找到自己的语言 */}
          <Segmented
            value={prefs.language}
            onChange={set("language")}
            options={[
              ["auto", t("跟随系统（{lang}）", { lang: systemLang })],
              ["zh", "中文"],
              ["en", "English"],
            ]}
          />
        </Row>
      </div>

      <footer className="flex shrink-0 items-center border-t border-line px-6 py-3">
        <span className="text-xs text-muted">{t("修改会立即保存")}</span>
        <div className="flex-1" />
        <button className="btn" onClick={() => confirm(t("恢复外观的默认设置？")) && void update({ ...DEFAULT_PREFS, customSkins: prefs.customSkins })}>
          {t("恢复默认")}
        </button>
      </footer>
    </div>
  );
}

function Row({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div>
      <div className="mb-2 text-sm font-medium">{label}</div>
      {children}
      {hint && <div className="mt-1 text-[11px] text-muted">{hint}</div>}
    </div>
  );
}

function Segmented<T extends string>({ value, onChange, options }: { value: T; onChange: (v: T) => void; options: [T, string][] }) {
  return (
    <div className="inline-flex flex-wrap gap-1 rounded-lg border border-line p-1" role="radiogroup">
      {options.map(([v, label]) => (
        <button
          key={v}
          role="radio"
          aria-checked={value === v}
          className={`rounded-md px-3 py-1 text-sm ${value === v ? "bg-accent text-on-accent" : "hover:bg-panel-2"}`}
          onClick={() => onChange(v)}
        >
          {label}
        </button>
      ))}
    </div>
  );
}
