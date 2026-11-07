import { useEffect, useState } from "react";
import { getShellSettings, setShellSettings, type ShellSettings } from "../bridge/electron";
import { t } from "../i18n";
import { Notice } from "./GeneralSettings";

// 设置 → 桌面（仅 Electron 版）：关闭窗口时的行为、系统通知、全局快捷键。
// 这些属于桌面壳，由壳保存在自己的目录里，和后端的配置无关；改了立即生效，不需要“保存”按钮。

const CLOSE_OPTIONS: [ShellSettings["closeBehavior"], string, string][] = [
  ["tray-when-running", "有任务在运行时藏到托盘", "没有任务在跑就直接退出；有任务在跑则缩到托盘继续运行，完成后通知你（默认）。"],
  ["tray", "总是藏到托盘", "关闭窗口不退出，随时从托盘图标回来；要退出请用托盘菜单。"],
  ["quit", "直接退出", "关闭窗口就退出应用，正在运行的任务会被停止。"],
];

export default function DesktopSettings() {
  const [s, setS] = useState<ShellSettings | null>(null);
  const [error, setError] = useState("");
  const [shortcut, setShortcut] = useState("");

  useEffect(() => {
    getShellSettings()
      .then((v) => {
        setS(v);
        if (v) setShortcut(v.quickAsk.shortcut);
      })
      .catch((e) => setError(String(e instanceof Error ? e.message : e)));
  }, []);

  const apply = async (patch: Parameters<typeof setShellSettings>[0]) => {
    setError("");
    try {
      const r = await setShellSettings(patch);
      setS(r.settings);
      setShortcut(r.settings.quickAsk.shortcut);
      if (r.error) setError(r.error);
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    }
  };

  if (!s) return <div className="p-6 text-sm text-muted">{error || t("加载中…")}</div>;
  const dirty = shortcut.trim() !== s.quickAsk.shortcut;
  return (
    <div className="flex-1 space-y-6 overflow-y-auto px-6 py-5">
      {error && <Notice tone="warn">{error}</Notice>}

      <section>
        <h3 className="mb-2 px-1 text-xs font-semibold tracking-wide text-muted">{t("关闭窗口时")}</h3>
        <div className="card divide-y divide-line px-5" role="radiogroup">
          {CLOSE_OPTIONS.map(([id, label, hint]) => (
            <label key={id} className="flex cursor-pointer items-start gap-3 py-3">
              <input type="radio" name="close-behavior" className="mt-1" checked={s.closeBehavior === id} onChange={() => void apply({ closeBehavior: id })} />
              <span>
                <span className="block text-sm font-medium">{t(label)}</span>
                <span className="mt-0.5 block text-[11px] leading-relaxed text-muted">{t(hint)}</span>
              </span>
            </label>
          ))}
        </div>
      </section>

      <section>
        <h3 className="mb-2 px-1 text-xs font-semibold tracking-wide text-muted">{t("通知")}</h3>
        <div className="card px-5">
          <label className="flex cursor-pointer items-start gap-3 py-3">
            <input type="checkbox" className="mt-1" checked={s.notifications} onChange={(e) => void apply({ notifications: e.target.checked })} />
            <span>
              <span className="block text-sm font-medium">{t("任务完成或需要确认时弹系统通知")}</span>
              <span className="mt-0.5 block text-[11px] leading-relaxed text-muted">{t("只在窗口不在眼前（隐藏、最小化或没有焦点）时弹；通知里不会显示对话内容，点击回到对应会话。")}</span>
            </span>
          </label>
        </div>
      </section>

      <section>
        <h3 className="mb-2 px-1 text-xs font-semibold tracking-wide text-muted">{t("快速提问")}</h3>
        <div className="card space-y-3 px-5 py-3">
          <label className="flex cursor-pointer items-start gap-3">
            <input type="checkbox" className="mt-1" checked={s.quickAsk.enabled} onChange={(e) => void apply({ quickAsk: { enabled: e.target.checked } })} />
            <span>
              <span className="block text-sm font-medium">{t("启用全局快捷键")}</span>
              <span className="mt-0.5 block text-[11px] leading-relaxed text-muted">{t("在任何程序里按下它，唤起 Mosaic、新建会话并聚焦输入框。")}</span>
            </span>
          </label>
          <div className="flex items-center gap-2">
            <input
              className="input w-72 font-mono"
              value={shortcut}
              disabled={!s.quickAsk.enabled}
              onChange={(e) => setShortcut(e.target.value)}
              spellCheck={false}
              aria-label={t("快捷键")}
              placeholder="CommandOrControl+Shift+M"
            />
            <button className="btn" disabled={!s.quickAsk.enabled || !dirty} onClick={() => void apply({ quickAsk: { shortcut: shortcut.trim() } })}>
              {t("应用")}
            </button>
          </div>
          <div className="text-[11px] text-muted">{t("写法：修饰键加按键，用 + 连接，例如 CommandOrControl+Shift+M、Alt+Space。")}</div>
        </div>
      </section>
    </div>
  );
}
