import { t } from "../i18n";
import { useUIPrefs } from "../store/uiPrefs";
import { updateIndicator, useUpdate } from "../store/updateStore";

// 窗口右上角的更新入口。平时是个低调的"检查更新"；有新版本时高亮并闪烁，看过更新内容后保持高亮不再闪；
// 下载安装期间显示进度。点击打开更新窗口（UpdateDialog）。
export default function UpdateButton() {
  const { info, seen, status, checking, autoError, applyQueued, openDialog } = useUpdate();
  const skipped = useUIPrefs((s) => s.prefs.skippedVersion);
  const autoEnabled = useUIPrefs((s) => s.prefs.updateCheck === "auto");
  const state = updateIndicator({ info, seen, status }, skipped);
  const pct = status.total > 0 ? Math.min(100, Math.round((status.done / status.total) * 100)) : 0;

  const base = "mr-2 inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs";
  if (state === "none") {
    // 检查失败与禁用状态不是“没更新”。明确显示在标题栏，避免用户误以为检测成功。
    const reason = autoEnabled ? (info?.disabled || autoError) : "";
    const failed = !!reason && !checking;
    const skippedLatest = !!info?.available && info.latest === skipped;
    const noAutomaticCheck = !autoEnabled;
    const hint = failed
      ? t("更新检查失败：{reason}；点击查看详情", { reason })
      : skippedLatest
        ? t("已跳过版本 {v}，点击仍可查看更新", { v: info!.latest })
        : noAutomaticCheck
          ? t("自动检查已关闭，点击手动检查")
          : t("检查更新");
    return (
      <button
        className={`${base} ${failed ? "bg-warn/10 font-medium text-warn hover:bg-warn/15" : "text-muted hover:text-fg"}`}
        onClick={openDialog}
        title={hint}
        aria-label={failed ? t("更新检查失败，点击重试") : hint}
        disabled={checking}
      >
        {failed && <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-warn" />}
        {checking ? t("正在检查…") : failed ? t("更新检查失败") : skippedLatest ? t("已跳过 {v}", { v: info!.latest }) : noAutomaticCheck ? t("手动检查更新") : t("检查更新")}
      </button>
    );
  }
  const label =
    state === "ready"
      ? applyQueued
        ? t("对话结束后自动更新")
        : t("新版本 {v} 已下载，点击安装", { v: status.version || info?.latest || "" })
      : state === "busy"
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
      title={state === "busy" ? t("更新在后台进行，点击查看进度") : state === "ready" ? (applyQueued ? t("已排队：当前对话全部结束后会自动安装并重启") : t("新版本已下载，点击选择是否现在安装")) : t("查看更新内容")}
    >
      {state !== "busy" && <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-on-accent" />}
      {label}
    </button>
  );
}
