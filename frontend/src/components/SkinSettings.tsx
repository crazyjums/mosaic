import { useEffect, useState } from "react";
import { t } from "../i18n";
import { applySkin, BUILTIN_SKINS, exportSkin, findSkin, newSkinId, parseSkin, SKIN_VARS, VAR_LABELS, type Palette, type Skin } from "../store/skins";
import { applyPrefs, useUIPrefs } from "../store/uiPrefs";

// 设置 → 外观 → 皮肤：选择内置 / 自定义皮肤，新建、编辑、导入、导出自定义皮肤。
//
// 编辑时实时预览：直接把草稿应用到页面，并临时把主题切到正在编辑的那一份（深色或浅色），
// 否则在深色模式下改浅色配色是看不到效果的。关闭编辑器时按保存的偏好恢复。

type Mode = "dark" | "light";

/** 当前实际显示的是深色还是浅色（主题为"跟随系统"时看系统设置）。 */
function resolvedMode(theme: "system" | "light" | "dark"): Mode {
  if (theme !== "system") return theme;
  return typeof matchMedia !== "undefined" && matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

const isBuiltin = (s: Skin) => BUILTIN_SKINS.some((b) => b.id === s.id);
const skinName = (s: Skin) => (isBuiltin(s) ? t(s.name) : s.name);

export default function SkinSettings() {
  const { prefs, update } = useUIPrefs();
  const mode = resolvedMode(prefs.theme);
  const skins = [...BUILTIN_SKINS, ...prefs.customSkins];
  const [editing, setEditing] = useState<Skin | null>(null);
  const [importing, setImporting] = useState(false);

  const select = (id: string) => void update({ skin: id });
  const startNew = () => {
    const base = findSkin(prefs.skin, prefs.customSkins);
    setImporting(false);
    setEditing({ ...structuredClone(base), id: newSkinId(), name: t("{name} 副本", { name: skinName(base) }) });
  };
  const saveSkin = (s: Skin) => {
    const exists = prefs.customSkins.some((c) => c.id === s.id);
    const customSkins = exists ? prefs.customSkins.map((c) => (c.id === s.id ? s : c)) : [...prefs.customSkins, s];
    setEditing(null);
    void update({ customSkins, skin: s.id });
  };
  const remove = (s: Skin) => {
    if (!confirm(t("删除皮肤「{name}」？", { name: s.name }))) return;
    setEditing(null);
    void update({ customSkins: prefs.customSkins.filter((c) => c.id !== s.id), skin: prefs.skin === s.id ? "default" : prefs.skin });
  };

  return (
    <div>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(9.5rem,1fr))] gap-3">
        {skins.map((s) => (
          <div key={s.id} className="group relative">
            <button
              className={`block w-full overflow-hidden rounded-lg border-2 text-left ${prefs.skin === s.id ? "border-accent" : "border-line hover:border-muted"}`}
              onClick={() => select(s.id)}
              aria-pressed={prefs.skin === s.id}
              title={skinName(s)}
            >
              <SkinPreview palette={s[mode]} />
              <div className="flex items-center gap-1 bg-panel px-2 py-1.5 text-xs">
                <span className="min-w-0 flex-1 truncate">{skinName(s)}</span>
                {prefs.skin === s.id && <span className="text-accent">✓</span>}
              </div>
            </button>
            {!isBuiltin(s) && (
              <button
                className="absolute right-1 top-1 hidden rounded bg-panel/90 px-1.5 py-0.5 text-[11px] text-fg shadow group-hover:block"
                onClick={() => {
                  setImporting(false);
                  setEditing(structuredClone(s));
                }}
              >
                {t("编辑")}
              </button>
            )}
          </div>
        ))}
      </div>

      <div className="mt-3 flex gap-2">
        <button className="btn" onClick={startNew}>
          {t("＋ 新建皮肤（基于当前皮肤）")}
        </button>
        <button
          className="btn"
          onClick={() => {
            setEditing(null);
            setImporting(!importing);
          }}
        >
          {t("导入…")}
        </button>
      </div>

      {importing && (
        <ImportBox
          onImport={(s) => {
            setImporting(false);
            void update({ customSkins: [...prefs.customSkins, s], skin: s.id });
          }}
        />
      )}
      {editing && (
        <SkinEditor
          key={editing.id}
          initial={editing}
          initialMode={mode}
          isNew={!prefs.customSkins.some((c) => c.id === editing.id)}
          onSave={saveSkin}
          onCancel={() => setEditing(null)}
          onDelete={() => remove(editing)}
        />
      )}
    </div>
  );
}

/** 皮肤缩略图：用皮肤自己的颜色画一个迷你界面（侧栏、消息、按钮），不依赖当前生效的变量。 */
function SkinPreview({ palette: c }: { palette: Palette }) {
  return (
    <div className="flex h-16 gap-1.5 p-1.5" style={{ background: c.bg }}>
      <div className="w-6 rounded-sm" style={{ background: c.panel, border: `1px solid ${c.border}` }} />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="h-1.5 w-3/4 rounded-full" style={{ background: c.fg }} />
        <div className="h-1.5 w-1/2 rounded-full" style={{ background: c.muted }} />
        <div className="h-3 rounded-sm" style={{ background: c["panel-2"], border: `1px solid ${c.think}` }} />
        <div className="mt-auto flex gap-1">
          <div className="flex h-3 w-8 items-center justify-center rounded-sm" style={{ background: c.accent }}>
            <div className="h-0.5 w-4 rounded-full" style={{ background: c["on-accent"] }} />
          </div>
          {(["ok", "warn", "danger"] as const).map((k) => (
            <div key={k} className="h-3 w-3 rounded-full" style={{ background: c[k] }} />
          ))}
        </div>
      </div>
    </div>
  );
}

function SkinEditor(props: {
  initial: Skin;
  initialMode: Mode;
  isNew: boolean;
  onSave: (s: Skin) => void;
  onCancel: () => void;
  onDelete: () => void;
}) {
  const [draft, setDraft] = useState<Skin>(props.initial);
  const [mode, setMode] = useState<Mode>(props.initialMode);
  const [exported, setExported] = useState<string | null>(null);

  // 实时预览：应用草稿，并临时切到正在编辑的那一份。卸载时按保存的偏好恢复。
  useEffect(() => {
    applySkin(draft);
    document.documentElement.dataset.theme = mode;
  }, [draft, mode]);
  useEffect(() => () => applyPrefs(useUIPrefs.getState().prefs), []);

  const setColor = (k: (typeof SKIN_VARS)[number], v: string) => setDraft((d) => ({ ...d, [mode]: { ...d[mode], [k]: v.toLowerCase() } }));
  const valid = draft.name.trim() !== "" && SKIN_VARS.every((k) => /^#[0-9a-f]{6}$/i.test(draft.dark[k]) && /^#[0-9a-f]{6}$/i.test(draft.light[k]));

  const doExport = async () => {
    const json = exportSkin(draft);
    setExported(json);
    try {
      await navigator.clipboard.writeText(json);
    } catch {
      // 剪贴板不可用时用户可以从下面的文本框复制
    }
  };

  return (
    <div className="mt-4 space-y-3 rounded-lg border border-accent/50 bg-panel-2 p-4">
      <div className="flex flex-wrap items-center gap-3">
        <input
          className="input w-56"
          value={draft.name}
          maxLength={40}
          onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          placeholder={t("皮肤名称")}
        />
        <div className="inline-flex gap-1 rounded-lg border border-line p-1">
          {(["dark", "light"] as const).map((m) => (
            <button key={m} className={`rounded-md px-3 py-1 text-sm ${mode === m ? "bg-accent text-on-accent" : "hover:bg-panel"}`} onClick={() => setMode(m)}>
              {m === "dark" ? t("深色") : t("浅色")}
            </button>
          ))}
        </div>
        <span className="text-[11px] text-muted">{t("修改会实时预览；切换深色 / 浅色分别编辑两套颜色。")}</span>
      </div>

      <div className="grid grid-cols-2 gap-x-6 gap-y-2 sm:grid-cols-3">
        {SKIN_VARS.map((k) => (
          <label key={k} className="flex items-center gap-2 text-xs">
            <input
              type="color"
              className="h-7 w-9 shrink-0 cursor-pointer rounded border border-line bg-transparent p-0"
              value={draft[mode][k]}
              onChange={(e) => setColor(k, e.target.value)}
              aria-label={t(VAR_LABELS[k])}
            />
            <span className="min-w-0 flex-1 truncate">{t(VAR_LABELS[k])}</span>
            <input
              className={`w-20 rounded border bg-bg px-1.5 py-0.5 font-mono text-[11px] outline-none ${/^#[0-9a-f]{6}$/i.test(draft[mode][k]) ? "border-line" : "border-danger"}`}
              value={draft[mode][k]}
              onChange={(e) => setColor(k, e.target.value.trim())}
              spellCheck={false}
            />
          </label>
        ))}
      </div>

      {exported !== null && (
        <div>
          <div className="mb-1 text-[11px] text-muted">{t("已复制到剪贴板，也可以从这里复制：")}</div>
          <textarea className="input h-28 font-mono text-[11px]" readOnly value={exported} onFocus={(e) => e.target.select()} />
        </div>
      )}

      <div className="flex items-center gap-2">
        {!props.isNew && (
          <button className="rounded-md px-3 py-1.5 text-sm text-danger hover:bg-danger/10" onClick={props.onDelete}>
            {t("删除")}
          </button>
        )}
        <button className="btn" onClick={() => void doExport()} disabled={!valid}>
          {t("导出")}
        </button>
        <div className="flex-1" />
        <button className="btn" onClick={props.onCancel}>
          {t("取消")}
        </button>
        <button
          className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50"
          onClick={() => props.onSave({ ...draft, name: draft.name.trim() })}
          disabled={!valid}
        >
          {t("保存并使用")}
        </button>
      </div>
    </div>
  );
}

function ImportBox({ onImport }: { onImport: (s: Skin) => void }) {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const submit = () => {
    let raw: unknown;
    try {
      raw = JSON.parse(text);
    } catch {
      setError(t("不是有效的 JSON"));
      return;
    }
    const s = parseSkin(raw);
    if (typeof s === "string") {
      setError(s);
      return;
    }
    onImport({ ...s, id: newSkinId() }); // 导入的总是新皮肤，不覆盖已有的
  };
  return (
    <div className="mt-4 space-y-2 rounded-lg border border-line bg-panel-2 p-4">
      <div className="text-xs text-muted">{t("粘贴别人导出的皮肤 JSON：")}</div>
      <textarea
        className="input h-32 font-mono text-[11px]"
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          setError("");
        }}
        placeholder={'{\n  "name": "My skin",\n  "dark": { "bg": "#101010", "accent": "#ff8800", ... },\n  "light": { ... }\n}'}
        spellCheck={false}
      />
      <div className="flex items-center gap-2">
        {error && <span className="text-xs text-danger">{error}</span>}
        <div className="flex-1" />
        <button className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" onClick={submit} disabled={!text.trim()}>
          {t("导入")}
        </button>
      </div>
    </div>
  );
}
