import type { ToolView } from "../store/reducer";
import { summarizeTurn } from "../store/turnSummary";
import { t } from "../i18n";
import { FileLink } from "./FileLink";

export function TurnSummary({ tools }: { tools: ToolView[] }) {
  if (!tools.length) return null;
  const summary = summarizeTurn(tools);
  return (
    <section className="space-y-2 rounded-card border border-line bg-panel px-4 py-3 text-xs" aria-label={t("本轮执行摘要")}>
      <h3 className="font-semibold text-fg">{t("本轮执行摘要")}</h3>
      {summary.files.length > 0 && <div><span className="text-muted">{t("工具报告的文件变更")}</span><ul className="mt-1 space-y-1">{summary.files.map((path) => <li key={path}><FileLink path={path} /></li>)}</ul></div>}
      {summary.commands.length > 0 && <div><span className="text-muted">{t("命令记录")}</span><ul className="mt-1 space-y-1">{summary.commands.map((cmd) => <li key={cmd.callId} className="break-all font-mono">{cmd.command} <span className="font-sans text-muted">{cmd.status === "done" ? t("已完成") : cmd.status === "denied" ? t("已拒绝") : cmd.status === "error" ? t("失败") : t("状态未知")}</span></li>)}</ul></div>}
      {summary.failed.length > 0 && <p className="text-danger">{t("{n} 次工具调用失败或被拒绝", { n: summary.failed.length })}</p>}
      <p className="text-muted">{t("仅汇总可识别的工具记录；命令或外部引擎产生的其他文件可能未列出。")}</p>
    </section>
  );
}
