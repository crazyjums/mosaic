import { useEffect } from "react";
import { backend } from "../bridge";
import { t } from "../i18n";
import { anyRunning } from "../store/activity";
import { useChat } from "../store/chatStore";
import { fmtMB, useUpdate } from "../store/updateStore";
import { useUIPrefs } from "../store/uiPrefs";
import ChangeLog, { ChangeSummary, LevelBadge } from "./ChangeLog";

// 更新窗口：显示新版本的更新内容（按版本、按类型分组），可以下载更新 / 稍后 / 跳过此版本。
// 没有新版本时显示"已是最新版本"，也可以重新检查。
//
// 更新分两步：先下载（后台进行，窗口随时可以最小化，进度显示在右上角的按钮上），下载完成后窗口会再弹出来，
// 问“现在安装还是稍后手动安装”。选现在安装时如果还有会话在运行，就排队等它们都结束再安装并重启，不打断任务。
export default function UpdateDialog() {
  const { info, status, error, checking, dialogOpen, version, applyQueued, check, download, applyNow, cancelQueued, closeDialog, skip } = useUpdate();
  const skipped = useUIPrefs((s) => s.prefs.skippedVersion);
  const running = useChat(anyRunning);
  const busy = status.state === "downloading" || status.state === "installing" || status.state === "restarting";
  const ready = status.state === "ready";

  useEffect(() => {
    if (!dialogOpen) return;
    // Esc 和点击空白处都只是最小化：下载、排队中的安装照常进行。
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeDialog();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dialogOpen, closeDialog]);

  if (!dialogOpen) return null;
  const available = !!info?.available;
  const pct = status.total > 0 ? Math.min(100, Math.round((status.done / status.total) * 100)) : 0;

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-6" onClick={closeDialog} role="dialog" aria-modal="true">
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
          <button
            className="shrink-0 rounded px-1.5 text-muted hover:text-fg"
            onClick={closeDialog}
            aria-label={busy || ready ? t("最小化") : t("关闭")}
            title={busy || ready ? t("最小化：更新在后台继续，进度显示在右上角") : t("关闭")}
          >
            {busy || ready ? "—" : "✕"}
          </button>
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
          {ready && (
            <div className="mb-3 rounded-lg border border-accent/40 bg-accent/10 px-3 py-2.5">
              <div className="font-medium">{t("新版本 {v} 已下载完成", { v: status.version || info?.latest || "" })}</div>
              {applyQueued ? (
                <div className="mt-1 text-xs text-muted">{t("已排队：当前对话全部结束后会自动安装并重启，不会打断正在做的事。")}</div>
              ) : (
                <div className="mt-1 text-xs text-muted">
                  {t("是否现在安装？安装后应用会自动重启。也可以稍后手动安装——点右上角的“新版本已下载”即可。")}
                  {running && <span className="mt-1 block text-warn">{t("有对话正在运行：选择“立即安装”会等它们结束后再安装。")}</span>}
                </div>
              )}
              {status.error && <div className="mt-1 text-xs text-danger">{t("上次安装失败：{e}", { e: status.error })}</div>}
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

        {busy && (
          <div className="flex items-center justify-end gap-2 border-t border-line px-5 py-3 text-sm">
            <span className="mr-auto text-xs text-muted">{t("可以最小化，更新在后台继续。")}</span>
            <button className="rounded-md border border-line px-3 py-1.5 hover:bg-panel-2" onClick={closeDialog}>
              {t("最小化")}
            </button>
          </div>
        )}
        {ready && (
          <div className="flex items-center justify-end gap-2 border-t border-line px-5 py-3 text-sm">
            {applyQueued ? (
              <>
                <button className="mr-auto rounded-md px-3 py-1.5 text-muted hover:text-fg" onClick={cancelQueued}>
                  {t("取消排队")}
                </button>
                <button className="rounded-md border border-line px-3 py-1.5 hover:bg-panel-2" onClick={closeDialog}>
                  {t("最小化")}
                </button>
              </>
            ) : (
              <>
                <button className="rounded-md border border-line px-3 py-1.5 hover:bg-panel-2" onClick={closeDialog}>
                  {t("稍后手动安装")}
                </button>
                <button className="rounded-md bg-accent px-3 py-1.5 font-medium text-on-accent hover:opacity-90" onClick={() => void applyNow()}>
                  {running ? t("运行结束后安装") : t("立即安装")}
                </button>
              </>
            )}
          </div>
        )}
        {!busy && !ready && (
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
                    className="rounded-md bg-accent px-3 py-1.5 font-medium text-on-accent hover:opacity-90"
                    onClick={() => void download()}
                    title={t("先在后台下载；下载完成后再问你是否现在安装")}
                  >
                    {t("下载更新")}
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
