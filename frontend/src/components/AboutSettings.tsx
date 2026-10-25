import { backend } from "../bridge";
import { t } from "../i18n";
import { useUIPrefs } from "../store/uiPrefs";
import { useUpdate } from "../store/updateStore";
import { Notice } from "./GeneralSettings";

// 设置 → 关于：版本号、检查更新、自动检查开关。
export default function AboutSettings() {
  const { version, info, checking, error, status, check, install } = useUpdate();
  const prefs = useUIPrefs((s) => s.prefs);
  const update = useUIPrefs((s) => s.update);
  const busy = status.state === "downloading" || status.state === "installing" || status.state === "restarting";

  return (
    <div className="flex-1 space-y-6 overflow-y-auto px-6 py-5">
      <section>
        <div className="text-lg font-semibold">myllmapp</div>
        <div className="mt-1 text-sm text-muted">{t("版本 {v}", { v: version || "—" })}</div>
      </section>

      <section>
        <div className="mb-2 text-sm font-medium">{t("检查更新")}</div>
        <div className="flex flex-wrap items-center gap-3">
          <button className="btn" onClick={() => void check(true)} disabled={checking || busy}>
            {checking ? t("正在检查…") : t("检查更新")}
          </button>
          {info && !checking && !error && !info.disabled && !info.available && <span className="text-sm text-ok">{t("已是最新版本")}</span>}
          {info?.available && !busy && (
            <>
              <span className="text-sm">{t("发现新版本 {v}", { v: info.latest })}</span>
              {info.manual ? (
                <button className="btn" onClick={() => void backend().openExternalURL(info.page || "")} disabled={!info.page}>
                  {t("前往下载页")}
                </button>
              ) : (
                <button className="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-on-accent hover:opacity-90" onClick={() => void install()}>
                  {t("立即更新")}
                </button>
              )}
            </>
          )}
        </div>
        {info?.disabled && <div className="mt-2"><Notice tone="info">{info.disabled}</Notice></div>}
        {error && <div className="mt-2 text-sm text-danger">{error}</div>}
        {info?.available && info.notes && <pre className="mt-3 max-h-48 overflow-auto whitespace-pre-wrap rounded-md border border-line bg-panel p-3 text-xs text-muted">{info.notes}</pre>}
        {info?.available && info.manual && <div className="mt-2 text-[11px] text-muted">{t("这个系统暂不支持自动安装，请到发布页下载新版本。")}</div>}
      </section>

      <section>
        <div className="mb-2 text-sm font-medium">{t("自动检查")}</div>
        <div className="inline-flex gap-1 rounded-lg border border-line p-1" role="radiogroup">
          {(
            [
              ["auto", t("启动时和每隔几小时自动检查")],
              ["off", t("只手动检查")],
            ] as const
          ).map(([v, label]) => (
            <button
              key={v}
              role="radio"
              aria-checked={prefs.updateCheck === v}
              className={`rounded-md px-3 py-1 text-sm ${prefs.updateCheck === v ? "bg-accent text-on-accent" : "hover:bg-panel-2"}`}
              onClick={() => void update({ updateCheck: v })}
            >
              {label}
            </button>
          ))}
        </div>
        <div className="mt-1 text-[11px] text-muted">
          {t("更新包经过签名校验：只有用发布者私钥签名的版本才会被安装。检查更新只会访问发布服务器，不会上传你的任何数据。")}
        </div>
        {prefs.skippedVersion && (
          <div className="mt-2 text-xs text-muted">
            {t("已跳过版本 {v}", { v: prefs.skippedVersion })}{" "}
            <button className="text-accent hover:underline" onClick={() => void update({ skippedVersion: "" })}>
              {t("恢复提示")}
            </button>
          </div>
        )}
      </section>
    </div>
  );
}
