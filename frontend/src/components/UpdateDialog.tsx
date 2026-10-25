import { useEffect } from "react";
import { backend } from "../bridge";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";
import { fmtMB, useUpdate } from "../store/updateStore";
import { useUIPrefs } from "../store/uiPrefs";
import ChangeLog, { ChangeSummary, LevelBadge } from "./ChangeLog";

// 更新窗口：显示新版本的更新内容（按版本、按类型分组），可以立即更新 / 稍后 / 跳过此版本。
// 没有新版本时显示"已是最新版本"，也可以重新检查。立即更新会下载安装并自动重启；agent 运行中时先不让点，不打断当前任务。
export default function UpdateDialog() {
  const { info, status, error, checking, dialogOpen, version, check, install, closeDialog, skip } = useUpdate();
  const skipped = useUIPrefs((s) => s.prefs.skippedVersion);
  const running = useChat((s) => s.running);
  const busy = status.state === "downloading" || status.state === "installing" || status.state === "restarting";

  useEffect(() => {
    if (!dialogOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) closeDialog();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dialogOpen, busy, closeDialog]);

  if (!dialogOpen) return null;
  const available = !!info?.available;
  const pct = status.total > 0 ? Math.min(100, Math.round((status.done / status.total) * 100)) : 0;

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-6" onClick={() => !busy && closeDialog()} role="dialog" aria-modal="true">
      <div className="flex max-h-[85vh] w-full max-w-xl flex-col shadow-pop rounded-modal border border-line bg-panel" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-start justify-between gap-3 border-b border-line px-5 py-4">
          <div className="min-w-0">
            <div className="text-[15px] font-semibold">{available ? t("发现新版本 {v}", { v: info!.latest }) : t("检查更新")}</div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
              <span>{t("当前 {v}", { v: info?.current || version || "—" })}</span>
              {available && <LevelBadge info={info!} />}
              {available && info!.releases && <ChangeSummary releases={info!.releases} />}
            </div>
          </div>
          {!busy && (
            <button className="shrink-0 rounded px-1.5 text-muted hover:text-fg" onClick={closeDialog} aria-label={t("关闭")}>
              ✕
            </button>
          )}
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4 text-sm">
          {checking && <div className="text-muted">{t("正在检查…")}</div>}
          {!checking && error && <div className="text-danger">{error}</div>}
          {!checking && !error && info?.disabled && <div className="text-muted">{info.disabled}</div>}
          {!checking && !error && info && !info.disabled && !available && (
            <div>
              <span className="text-ok">{t("已是最新版本")}</span>
              <div className="mt-2 text-xs text-muted">{t("更新包经过签名校验，检查更新只会访问发布服务器，不会上传你的任何数据。")}</div>
            </div>
          )}
          {available && (
            <div className="space-y-2">
              {skipped === info!.latest && <div className="text-xs text-muted">{t("你之前选择了跳过这个版本，不会再自动提示；仍然可以在这里更新。")}</div>}
              <ChangeLog info={info!} />
            </div>
          )}
        </div>

        {status.state === "error" && <div className="px-5 pb-2 text-xs text-danger">{status.error}</div>}
        {busy && (
          <div className="px-5 pb-3">
            <div className="mb-1 text-xs tabular-nums text-muted">
              {status.state === "downloading" && t("正在下载 {p}%（{a} / {b}）", { p: pct, a: fmtMB(status.done), b: fmtMB(status.total) })}
              {status.state === "installing" && t("正在安装…")}
              {status.state === "restarting" && t("正在重启…")}
            </div>
            <div className="h-1 overflow-hidden rounded-full bg-line">
              <div className="h-full bg-accent transition-all" style={{ width: `${Math.max(pct, 2)}%` }} />
            </div>
          </div>
        )}

        {!busy && (
          <div className="flex items-center justify-end gap-2 border-t border-line px-5 py-3 text-sm">
            {available ? (
              <>
                {skipped !== info!.latest && (
                  <button className="mr-auto rounded-md px-3 py-1.5 text-muted hover:text-fg" onClick={skip} title={t("这个版本不再提示")}>
                    {t("跳过此版本")}
                  </button>
                )}
                <button className="rounded-md border border-line px-3 py-1.5 hover:bg-panel-2" onClick={closeDialog}>
                  {t("稍后")}
                </button>
                {info!.manual ? (
                  <button className="rounded-md bg-accent px-3 py-1.5 font-medium text-on-accent hover:opacity-90 disabled:opacity-50" onClick={() => void backend().openExternalURL(info!.page || "")} disabled={!info!.page}>
                    {t("前往下载页")}
                  </button>
                ) : (
                  <button
                    className="rounded-md bg-accent px-3 py-1.5 font-medium text-on-accent hover:opacity-90 disabled:opacity-50"
                    onClick={() => void install()}
                    disabled={running}
                    title={running ? t("agent 正在运行，等这一轮结束再更新") : t("下载并安装，完成后自动重启")}
                  >
                    {t("立即更新")}
                  </button>
                )}
              </>
            ) : (
              <>
                <button className="rounded-md border border-line px-3 py-1.5 hover:bg-panel-2 disabled:opacity-50" onClick={() => void check(true)} disabled={checking}>
                  {t("重新检查")}
                </button>
                <button className="rounded-md bg-accent px-3 py-1.5 font-medium text-on-accent hover:opacity-90" onClick={closeDialog}>
                  {t("关闭")}
                </button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
