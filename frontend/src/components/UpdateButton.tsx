import { t } from "../i18n";
import { useUIPrefs } from "../store/uiPrefs";
import { updateIndicator, useUpdate } from "../store/updateStore";

// 窗口右上角的更新入口。平时是个低调的"检查更新"；有新版本时高亮并闪烁，看过更新内容后保持高亮不再闪；
// 下载安装期间显示进度。点击打开更新窗口（UpdateDialog）。
export default function UpdateButton() {
  const { info, seen, status, checking, openDialog } = useUpdate();
  const skipped = useUIPrefs((s) => s.prefs.skippedVersion);
  const state = updateIndicator({ info, seen, status }, skipped);
  const pct = status.total > 0 ? Math.min(100, Math.round((status.done / status.total) * 100)) : 0;

  const base = "mr-2 rounded-md px-2 py-1 text-xs";
  if (state === "none") {
    return (
      <button className={`${base} text-muted hover:text-fg`} onClick={openDialog} title={t("检查更新")} disabled={checking}>
        {checking ? t("正在检查…") : t("检查更新")}
      </button>
    );
  }
  const label =
    state === "busy"
      ? status.state === "downloading"
        ? t("正在下载 {p}%", { p: pct })
        : status.state === "installing"
          ? t("正在安装…")
          : t("正在重启…")
      : t("发现新版本 {v}", { v: info?.latest ?? "" });
  return (
    <button
      className={`${base} bg-accent font-medium text-on-accent hover:opacity-90 ${state === "blink" ? "update-blink" : ""}`}
      onClick={openDialog}
      title={t("查看更新内容")}
    >
      {label}
    </button>
  );
}
