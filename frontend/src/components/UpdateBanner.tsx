import { useState } from "react";
import { backend } from "../bridge";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";
import { fmtMB, shouldShowBanner, useUpdate } from "../store/updateStore";
import { useUIPrefs } from "../store/uiPrefs";

// 窗口顶部的"发现新版本"横幅。可以：立即更新 / 查看更新说明 / 稍后（本次运行不再提示）/ 跳过此版本。
// 立即更新会下载安装并自动重启；agent 运行中时先提示，不打断当前任务。
export default function UpdateBanner() {
  const { info, status, dismissed, install, dismiss, skip } = useUpdate();
  const skipped = useUIPrefs((s) => s.prefs.skippedVersion);
  const running = useChat((s) => s.running);
  const [notes, setNotes] = useState(false);

  if (!shouldShowBanner({ info, dismissed, status }, skipped) || !info) return null;
  const busy = status.state === "downloading" || status.state === "installing" || status.state === "restarting";
  const pct = status.total > 0 ? Math.min(100, Math.round((status.done / status.total) * 100)) : 0;

  return (
    <div className="border-b border-accent/30 bg-accent/10 px-5 py-2 text-sm" role="status">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="font-medium">{t("发现新版本 {v}", { v: info.latest })}</span>
        <span className="text-xs text-muted">{t("当前 {v}", { v: info.current })}</span>
        {info.notes && !busy && (
          <button className="text-xs text-accent hover:underline" onClick={() => setNotes(!notes)}>
            {notes ? t("收起更新说明") : t("更新说明")}
          </button>
        )}
        <div className="flex-1" />
        {busy ? (
          <span className="text-xs tabular-nums text-muted">
            {status.state === "downloading" && t("正在下载 {p}%（{a} / {b}）", { p: pct, a: fmtMB(status.done), b: fmtMB(status.total) })}
            {status.state === "installing" && t("正在安装…")}
            {status.state === "restarting" && t("正在重启…")}
          </span>
        ) : info.manual ? (
          <button className="rounded-md bg-accent px-3 py-1 text-xs font-medium text-on-accent hover:opacity-90" onClick={() => void backend().openExternalURL(info.page || "")} disabled={!info.page}>
            {t("前往下载页")}
          </button>
        ) : (
          <button
            className="rounded-md bg-accent px-3 py-1 text-xs font-medium text-on-accent hover:opacity-90 disabled:opacity-50"
            onClick={() => void install()}
            disabled={running}
            title={running ? t("agent 正在运行，等这一轮结束再更新") : t("下载并安装，完成后自动重启")}
          >
            {t("立即更新")}
          </button>
        )}
        {!busy && (
          <>
            <button className="text-xs text-muted hover:text-fg" onClick={dismiss}>
              {t("稍后")}
            </button>
            <button className="text-xs text-muted hover:text-fg" onClick={skip} title={t("这个版本不再提示")}>
              {t("跳过此版本")}
            </button>
          </>
        )}
      </div>
      {status.state === "error" && <div className="mt-1 text-xs text-danger">{status.error}</div>}
      {busy && status.state === "downloading" && (
        <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-line">
          <div className="h-full bg-accent transition-all" style={{ width: `${Math.max(pct, 2)}%` }} />
        </div>
      )}
      {notes && info.notes && <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap rounded-md bg-panel p-2 text-xs text-muted">{info.notes}</pre>}
    </div>
  );
}
