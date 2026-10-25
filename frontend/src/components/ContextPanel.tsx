import { useEffect, useState } from "react";
import { backend, type ContextBreakdown, type ContextItem, type ContextPart } from "../bridge";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";
import { contextTokens, fmtTokens, modelMeta } from "../store/metrics";
import { useUIPrefs } from "../store/uiPrefs";

// 右侧的"上下文明细"面板：下一次请求会发给模型什么、各占多少。
//
// 两个数字来源不同：
//   - 顶部的"最近一次请求"是厂商返回的真实用量（准确）；
//   - 下面的分类和"占用最多"是后端按字符估算的（core/contextinfo.go），用来看出"大头在哪"，
//     例如某次 read_file 返回了一大段内容。两者有差距是正常的。

// 每类内容的名称（中文原文，渲染时翻译）和颜色（CSS 变量，跟随皮肤）。
const KINDS: Record<ContextPart["kind"], { label: string; color: string }> = {
  system: { label: "系统提示词", color: "var(--muted)" },
  tools: { label: "工具定义", color: "color-mix(in srgb, var(--accent) 45%, var(--muted))" },
  user: { label: "用户消息", color: "var(--accent)" },
  assistant: { label: "模型回答", color: "var(--fg)" },
  thinking: { label: "思考过程", color: "var(--think)" },
  tool_call: { label: "工具调用", color: "var(--warn)" },
  tool_result: { label: "工具结果", color: "var(--ok)" },
  image: { label: "图片", color: "var(--danger)" },
};

function itemLabel(it: ContextItem): string {
  switch (it.kind) {
    case "system":
      return t("系统提示词");
    case "user":
      return t("第 {n} 条用户消息", { n: it.index ?? 0 });
    case "assistant":
      return t("第 {n} 次回答", { n: it.index ?? 0 });
    case "thinking":
      return t("第 {n} 次回答的思考", { n: it.index ?? 0 });
    case "tool_call":
      return t("调用 {tool}", { tool: it.tool || "?" });
    case "tool_result":
      return t("{tool} 的结果", { tool: it.tool || t("工具") });
    case "image":
      return t("图片");
    default:
      return t(KINDS[it.kind]?.label ?? it.kind);
  }
}

export default function ContextPanel() {
  const show = useUIPrefs((s) => s.prefs.contextPanel);
  const sessionId = useChat((s) => s.sessionId);
  // 会话切换、每次模型调用结束（时间线变化且不在流式输出中）、一轮结束时刷新。
  const refreshKey = useChat((s) => {
    const last = s.timeline[s.timeline.length - 1];
    const streaming = last?.kind === "assistant" && last.streaming;
    return `${s.sessionId}|${s.timeline.length}|${streaming}|${s.lastTurnMs}`;
  });
  const actual = useChat(contextTokens);
  const ctxWindow = useChat((s) => modelMeta(s.profiles, s.provider, s.model)?.context_window);
  const [data, setData] = useState<ContextBreakdown | null>(null);

  useEffect(() => {
    if (!show) return;
    const timer = setTimeout(() => {
      void backend()
        .getContextBreakdown()
        .then(setData)
        .catch(() => {});
    }, 300); // 流式输出期间事件很密，稍等再查，避免反复请求
    return () => clearTimeout(timer);
  }, [show, refreshKey, sessionId]);

  if (!show) return null;
  const parts = data?.parts ?? [];
  const total = data?.total ?? 0;
  const largest = data?.largest ?? [];

  return (
    <aside className="hidden w-72 shrink-0 flex-col overflow-y-auto border-l border-line bg-panel/40 px-4 py-4 text-xs xl:flex">
      <div className="mb-3 flex items-center">
        <span className="text-sm font-semibold">{t("上下文明细")}</span>
        <div className="flex-1" />
        <button className="text-muted hover:text-fg" title={t("隐藏（在标题栏可以重新打开）")} onClick={() => void useUIPrefs.getState().update({ contextPanel: false })}>
          ✕
        </button>
      </div>

      {/* 真实值：厂商返回的最近一次请求的上下文大小 */}
      <div className="rounded-lg border border-line bg-panel px-3 py-2.5">
        <div className="text-[11px] text-muted">{t("最近一次请求（厂商返回）")}</div>
        <div className="mt-0.5 text-lg font-semibold tabular-nums">
          {actual !== null ? fmtTokens(actual) : "—"}
          {ctxWindow ? <span className="text-xs font-normal text-muted"> / {fmtTokens(ctxWindow)}</span> : null}
        </div>
        {actual !== null && ctxWindow ? (
          <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-line">
            <div
              className={`h-full ${actual / ctxWindow >= 0.85 ? "bg-danger" : actual / ctxWindow >= 0.6 ? "bg-warn" : "bg-ok"}`}
              style={{ width: `${Math.max(2, Math.min(100, (actual / ctxWindow) * 100))}%` }}
            />
          </div>
        ) : null}
        {actual === null && <div className="mt-1 text-[11px] text-muted">{t("发送消息后显示")}</div>}
      </div>

      {/* 估算的组成 */}
      <div className="mt-4 flex items-baseline">
        <span className="font-medium">{t("组成（估算）")}</span>
        <div className="flex-1" />
        <span className="tabular-nums text-muted">≈ {fmtTokens(total)}</span>
      </div>
      {total > 0 && (
        <div className="mt-2 flex h-2.5 overflow-hidden rounded-full bg-line" role="img" aria-label={t("组成（估算）")}>
          {parts.map((p) => (
            <div key={p.kind} style={{ width: `${(p.tokens / total) * 100}%`, background: KINDS[p.kind].color }} title={`${t(KINDS[p.kind].label)} ${fmtTokens(p.tokens)}`} />
          ))}
        </div>
      )}
      <ul className="mt-2 space-y-1">
        {parts.map((p) => (
          <li key={p.kind} className="flex items-center gap-2">
            <span className="h-2.5 w-2.5 shrink-0 rounded-sm" style={{ background: KINDS[p.kind].color }} />
            <span className="min-w-0 flex-1 truncate">
              {t(KINDS[p.kind].label)}
              <span className="text-muted"> ×{p.count}</span>
            </span>
            <span className="tabular-nums">{fmtTokens(p.tokens)}</span>
            <span className="w-9 text-right tabular-nums text-muted">{total ? Math.round((p.tokens / total) * 100) : 0}%</span>
          </li>
        ))}
      </ul>
      {data && (
        <div className="mt-2 text-[11px] text-muted">
          {t("{m} 条消息 · {n} 个工具", { m: data.messages, n: data.tool_count })}
        </div>
      )}

      {/* 占用最多的几项 */}
      {largest.length > 0 && (
        <>
          <div className="mt-4 font-medium">{t("占用最多")}</div>
          <ul className="mt-2 space-y-1.5">
            {largest.map((it, i) => (
              <li key={i} className="rounded-md border border-line bg-panel px-2 py-1.5" title={it.detail}>
                <div className="flex items-center gap-2">
                  <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: KINDS[it.kind]?.color }} />
                  <span className="min-w-0 flex-1 truncate">{itemLabel(it)}</span>
                  <span className="tabular-nums">{fmtTokens(it.tokens)}</span>
                </div>
                {it.detail && <div className="mt-0.5 truncate pl-4 font-mono text-[10px] text-muted">{it.detail}</div>}
              </li>
            ))}
          </ul>
        </>
      )}

      <p className="mt-4 text-[10px] leading-relaxed text-muted">
        {t("组成按字符数估算（英文约 4 个字符 / token，中文约 1 个字 / token，图片按 1000 计），用来看出大头在哪；准确的总数以上面厂商返回的为准。")}
      </p>
    </aside>
  );
}
