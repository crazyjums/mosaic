import { t } from "../i18n";
import { useChat } from "../store/chatStore";
import { itemsFor } from "../store/queue";

// 待发送队列：agent 运行时发出的消息先排在这里。每条可以删除、移到最前面；发送失败的可以重试。
// "停止"只取消当前这一轮，队列保留：这里会明确写出"已暂停"和原因，点"继续发送"才会接着发。
export default function QueuePanel() {
  // 分别订阅需要的字段：不要订阅整个状态，否则流式输出的每一个片段都会重渲染这里
  const queue = useChat((s) => s.queue);
  const pausedMap = useChat((s) => s.paused);
  const sessionId = useChat((s) => s.sessionId);
  const running = useChat((s) => s.running);
  const removeQueued = useChat((s) => s.removeQueued);
  const moveQueuedFirst = useChat((s) => s.moveQueuedFirst);
  const retryQueued = useChat((s) => s.retryQueued);
  const resumeQueue = useChat((s) => s.resumeQueue);
  const sid = sessionId ?? "";
  const items = itemsFor({ queue, paused: pausedMap }, sid);
  if (items.length === 0) return null;
  const paused = !!pausedMap[sid];
  const failed = items.some((m) => m.status === "failed");

  const headline = failed
    ? t("有 {n} 条消息发送失败，处理后才会继续发送后面的", { n: items.filter((m) => m.status === "failed").length })
    : paused
      ? t("队列已暂停（你停止了当前这一轮，或这一轮出错了）：{n} 条消息仍在待发送", { n: items.length })
      : running
        ? t("待发送 {n} 条：当前这一轮结束后按顺序发出", { n: items.length })
        : t("待发送 {n} 条", { n: items.length });

  return (
    <section className="card overflow-hidden" aria-label={t("待发送队列")}>
      <header className={`flex items-center gap-2 px-3 py-1.5 text-xs ${failed ? "bg-danger/10 text-danger" : paused ? "bg-warn/10 text-warn" : "bg-accent-soft text-fg"}`}>
        <span className="min-w-0 flex-1 truncate" role="status">
          {headline}
        </span>
        {paused && !failed && (
          <button className="shrink-0 rounded-ctl bg-accent px-2.5 py-0.5 font-medium text-on-accent hover:bg-accent-hover" onClick={() => void resumeQueue()}>
            {t("继续发送")}
          </button>
        )}
      </header>
      <ol className="max-h-40 divide-y divide-line overflow-y-auto">
        {items.map((m, i) => (
          <li key={m.id} className="flex items-center gap-2 px-3 py-1.5 text-sm">
            <span className="w-4 shrink-0 text-right text-[11px] tabular-nums text-muted">{i + 1}</span>
            <span className="min-w-0 flex-1">
              <span className="block truncate" title={m.text}>
                {m.text || t("（只有附件）")}
              </span>
              {m.status === "failed" && m.error && <span className="block truncate text-[11px] text-danger" title={m.error}>{m.error}</span>}
            </span>
            {m.attachments.length > 0 && <span className="shrink-0 rounded-full border border-line px-1.5 text-[10px] text-muted">📎 {m.attachments.length}</span>}
            {m.status === "sending" && <span className="shrink-0 text-[11px] text-muted">{t("发送中…")}</span>}
            {m.status === "failed" && (
              <button className="shrink-0 rounded-ctl px-1.5 py-0.5 text-xs text-accent hover:bg-panel-2" onClick={() => void retryQueued(m.id)}>
                {t("重试")}
              </button>
            )}
            {m.status === "queued" && i > 0 && (
              <button className="shrink-0 rounded-ctl px-1.5 py-0.5 text-xs text-muted hover:bg-panel-2 hover:text-fg" onClick={() => moveQueuedFirst(m.id)} title={t("移到最前面（只调整顺序，不会打断正在运行的这一轮）")}>
                {t("置前")}
              </button>
            )}
            <button className="shrink-0 rounded-ctl px-1.5 py-0.5 text-xs text-muted hover:bg-panel-2 hover:text-danger" onClick={() => removeQueued(m.id)} aria-label={t("删除这条待发送的消息")}>
              ✕
            </button>
          </li>
        ))}
      </ol>
    </section>
  );
}
