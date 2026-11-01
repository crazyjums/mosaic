import { t } from "../i18n";
import { useUIPrefs } from "../store/uiPrefs";
import { updateIndicator, useUpdate } from "../store/updateStore";

// 窗口右上角的更新入口。平时是个低调的"检查更新"；有新版本时高亮并闪烁，看过更新内容后保持高亮不再闪；
// 下载安装期间显示进度。点击打开更新窗口（UpdateDialog）。
export default function UpdateButton() {
  const { info, seen, status, checking, autoError, openDialog } = useUpdate();
  const skipped = useUIPrefs((s) => s.prefs.skippedVersion);
  const autoEnabled = useUIPrefs((s) => s.prefs.updateCheck === "auto");
  const state = updateIndicator({ info, seen, status }, skipped);
  const pct = status.total > 0 ? Math.min(100, Math.round((status.done / status.total) * 100)) : 0;

  const base = "mr-2 inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs";
  if (state === "none") {
    const failed = autoEnabled && !!autoError && !checking;
    return (
      <button
        className={`${base} ${failed ? "text-warn hover:text-fg" : "text-muted hover:text-fg"}`}
        onClick={openDialog}
        title={failed ? t("自动检查更新失败，点击重试：{reason}", { reason: autoError }) : t("检查更新")}
        disabled={checking}
      >
        {failed && <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-warn" />}
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
      className={`${base} bg-accent font-semibold text-on-accent shadow-sm ring-2 ring-accent/25 hover:opacity-90 ${state === "blink" ? "update-blink" : ""}`}
      onClick={openDialog}
      title={t("查看更新内容")}
    >
      {state !== "busy" && <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-on-accent" />}
      {label}
    </button>
  );
}
