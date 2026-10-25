import { useEffect, useState } from "react";
import { backend, type ReleaseNotes } from "../bridge";
import { t } from "../i18n";
import { useUIPrefs } from "../store/uiPrefs";
import { useUpdate } from "../store/updateStore";
import { Notice } from "./GeneralSettings";
import MosaicMark from "./MosaicMark";
import { ISSUES_URL, LICENSE_URL, RELEASES_URL, REPO_URL } from "../store/project";
import ChangeLog, { ChangeSummary, LevelBadge, Release } from "./ChangeLog";

// 设置 → 关于：项目介绍、开源地址、检查更新、版本历史、自动检查开关。
const FEATURES: [string, string][] = [
  ["多厂商，也能用 ChatGPT 账号", "OpenAI 兼容、Anthropic，或直接登录 ChatGPT 账号使用套餐额度；多个模型可以组成故障转移组。"],
  ["看得见的 agent", "思考、工具调用、权限确认全过程可视化；写文件、执行命令前先问你。"],
  ["技能与 MCP", "把常用做法写成技能，输入 / 调用；用 MCP 接入更多工具。"],
  ["用量与上下文", "按天统计 token 和花费，上下文占用一目了然，附件和长会话都能处理。"],
];

export default function AboutSettings() {
  const { version, info, checking, error, status, check, install } = useUpdate();
  const prefs = useUIPrefs((s) => s.prefs);
  const update = useUIPrefs((s) => s.update);
  const [history, setHistory] = useState<ReleaseNotes[]>([]);
  useEffect(() => {
    backend().getVersionHistory().then(setHistory).catch(() => setHistory([]));
  }, []);
  const busy = status.state === "downloading" || status.state === "installing" || status.state === "restarting";

  return (
    <div className="flex-1 space-y-6 overflow-y-auto px-6 py-5">
      {/* 名片：Logo、名称、版本、一句话介绍 */}
      <section className="card flex flex-col gap-5 p-5 sm:flex-row sm:items-center">
        <MosaicMark size="lg" state="static" decorative={false} className="shrink-0" />
        <div className="min-w-0">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h3 className="text-2xl font-semibold tracking-tight">Mosaic</h3>
            <span className="rounded-full border border-line px-2 py-0.5 text-xs text-muted">{t("版本 {v}", { v: version || "—" })}</span>
          </div>
          <p className="mt-1.5 text-sm leading-relaxed text-muted">
            {t("一个桌面端的 AI 编码工作台：连接你自己的大模型，让它在你选定的工作区里读代码、改文件、跑命令——每一步都看得见，危险操作先问你。")}
          </p>
        </div>
      </section>

      {/* 它能做什么 */}
      <section>
        <div className="mb-2 text-sm font-medium">{t("它能做什么")}</div>
        <div className="grid gap-3 sm:grid-cols-2">
          {FEATURES.map(([title, desc]) => (
            <div key={title} className="rounded-card border border-line bg-panel-2/60 px-4 py-3">
              <div className="text-sm font-medium">{t(title)}</div>
              <div className="mt-0.5 text-xs leading-relaxed text-muted">{t(desc)}</div>
            </div>
          ))}
        </div>
      </section>

      {/* 链接：开源地址最显眼 */}
      <section>
        <div className="mb-2 text-sm font-medium">{t("开源与反馈")}</div>
        <div className="flex flex-wrap items-center gap-2">
          <button className="btn-primary" onClick={() => void backend().openExternalURL(REPO_URL)} title={REPO_URL}>
            {t("GitHub 仓库")} ↗
          </button>
          <button className="btn" onClick={() => void backend().openExternalURL(ISSUES_URL)}>
            {t("反馈问题")}
          </button>
          <button className="btn" onClick={() => void backend().openExternalURL(RELEASES_URL)}>
            {t("发布页")}
          </button>
          <button className="btn" onClick={() => void backend().openExternalURL(LICENSE_URL)}>
            {t("MIT 开源协议")}
          </button>
        </div>
        <div className="mt-1.5 select-text font-mono text-[11px] text-muted">{REPO_URL}</div>
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
              <LevelBadge info={info} />
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
        {info?.available && (
          <div className="mt-3 space-y-2">
            {info.releases && <ChangeSummary releases={info.releases} />}
            <ChangeLog info={info} />
          </div>
        )}
        {info?.available && info.manual && <div className="mt-2 text-[11px] text-muted">{t("这个系统暂不支持自动安装，请到发布页下载新版本。")}</div>}
      </section>

      {history.length > 0 && (
        <section>
          <div className="mb-2 text-sm font-medium">{t("版本历史")}</div>
          <div className="space-y-2">
            {history.map((r, i) => (
              <Release key={r.version} r={r} open={i === 0} mark={r.version === version ? t("当前") : undefined} />
            ))}
          </div>
        </section>
      )}

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
