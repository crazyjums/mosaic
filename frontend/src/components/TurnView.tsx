import { useState } from "react";
import { useChat } from "../store/chatStore";
import type { AssistantItem } from "../store/reducer";
import { useUIPrefs } from "../store/uiPrefs";
import { fmtDuration } from "../store/metrics";
import { t } from "../i18n";
import AssistantMessage, { HandoffMark } from "./AssistantMessage";
import ImageBlock from "./ImageBlock";
import MosaicMark from "./MosaicMark";
import Markdown, { copyText } from "./Markdown";
import NavIcon from "./Icons";
import { thinkingSummary } from "../store/thinkingSummary";
import ScholarThinking from "./ScholarThinking";
import ToolCallCard from "./ToolCallCard";

// 一轮对话里模型的全部输出（连续的若干条 assistant 消息，一条 = 一个 step）。
//
// 为什么要分组折叠：agent 干活时会有十几步思考和工具调用，全部平铺出来，最终答案被淹没在最底下。
// 默认（设置 → 外观 → 思考与工具调用 = 默认折叠）：
//   - 过程（思考、工具调用、中间步骤的说明）折叠成一行摘要，点击展开；
//   - 模型工作期间显示思考动画；开始输出最终回答时动画消失，回答直接显示；
//   - 等待确认的工具调用始终显示在外面，免得用户找不到要确认的是什么。
//
// "最终回答"= 最后一条消息里的正文和图片，且这条消息没有工具调用（有工具调用说明还没答完）。
export default function TurnView({ items, live }: { items: AssistantItem[]; live: boolean }) {
  const pref = useUIPrefs((s) => s.prefs.process);
  const tools = useChat((s) => s.tools);
  const [userOpen, setUserOpen] = useState<boolean | null>(null);
  const open = userOpen ?? pref === "expanded";

  const last = items[items.length - 1];
  const lastHasTool = last.blocks.some((b) => b.type === "tool");
  const lastBlock = last.blocks[last.blocks.length - 1];
  // 正在输出正文（最后一块是文字）时，把它当作最终回答直接显示；如果后面又出现工具调用，它会自动回到过程里。
  const answering = !lastHasTool && (!live || lastBlock?.type === "text");
  const answer = answering ? last.blocks.filter((b) => b.type === "text" || b.type === "image") : [];

  const allTools = items.flatMap((it) => it.blocks.flatMap((b) => (b.type === "tool" && tools[b.callId] ? [tools[b.callId]] : [])));
  const failed = allTools.filter((x) => x.status === "error" || x.status === "denied").length;
  const thoughts = items.filter((it) => it.blocks.some((b) => b.type === "thinking")).length;
  const hasProcess = allTools.length > 0 || thoughts > 0 || items.length > 1 || (answer.length === 0 && last.blocks.length > 0);
  const waiting = allTools.filter((x) => x.status === "awaiting_permission");

  const handoffs = items.reduce((n, it) => n + (it.route?.handedOn.length ?? 0), 0);
  let stats: string;
  const title = allTools.length > 0 ? t("思考与执行") : t("思考过程");
  if (allTools.length > 0) stats = t("{n} 步 · {m} 次工具调用", { n: items.length, m: allTools.length });
  else stats = t("{n} 步", { n: items.length });
  if (handoffs > 0) stats += ` · ${t("转交 {n} 次", { n: handoffs })}`;
  if (last.turnMs) stats += ` · ${fmtDuration(last.turnMs)}`;
  const summary = `${title} · ${stats}`;

  return (
    <div className="space-y-3">
      {hasProcess &&
        (open ? (
          // 展开后的过程区：整块有浅色底和细边框，和下面的"最终回答"卡片分开
          <section className="space-y-2.5 rounded-card border border-accent/25 bg-accent-soft/40 p-3" aria-label={title}>
            <div className="flex items-center gap-2.5 px-1">
              <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-accent text-on-accent" aria-hidden="true">
                <NavIcon name="general" />
              </span>
              <span className="text-sm font-semibold">{title}</span>
              <span className="min-w-0 truncate text-xs text-muted">· {stats}</span>
              {failed > 0 && (
                <span className="shrink-0 rounded-full bg-danger/10 px-2 py-0.5 text-xs font-medium text-danger">{t("{n} 次失败", { n: failed })}</span>
              )}
              <div className="flex-1" />
              <button className="shrink-0 rounded-full bg-accent px-3 py-1 text-xs font-medium text-on-accent hover:bg-accent-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent" onClick={() => setUserOpen(false)}>
                {t("收起")} ▴
              </button>
            </div>
            {items.map((it, i) => (
              <AssistantMessage key={it.id} item={it} omitAnswer={i === items.length - 1 && answer.length > 0} />
            ))}
          </section>
        ) : (
          <button
            className="flex w-full items-center gap-2.5 rounded-ctl border border-line bg-panel px-3 py-2 text-left text-xs text-muted shadow-card hover:border-accent/40 hover:text-fg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            onClick={() => setUserOpen(true)}
            title={t("展开查看每一步的思考和工具调用")}
          >
            <MosaicMark size="xs" />
            <span className="min-w-0 flex-1 truncate">{summary}</span>
            {failed > 0 && <span className="rounded-full bg-danger/10 px-2 py-0.5 font-medium text-danger">{t("{n} 次失败", { n: failed })}</span>}
            <span className="shrink-0 text-gold-ink">{t("展开")} ▾</span>
          </button>
        ))}
      {/* 只有最终回答、没有过程可折叠时，转交的标记也要让人看得到 */}
      {!hasProcess && handoffs > 0 && (
        <div className="flex items-center text-[11px] text-muted">
          {last.provider}/{last.model}
          {last.route && <span className="ml-1">[{last.route.group}]</span>}
          {last.route && <HandoffMark route={last.route} />}
        </div>
      )}
      {!open && waiting.map((x) => <ToolCallCard key={x.callId} tool={x} />)}
      {/* 工作中、还没开始输出最终回答：显示动画 */}
      {live && answer.length === 0 && waiting.length === 0 && (
        <ScholarThinking since={last.startedAt} thinking={open ? null : thinkingSummary(items)} label={allTools.some((x) => x.status === "running") ? t("正在执行工具…") : undefined} />
      )}
      {answer.length > 0 && (open && hasProcess ? <AnswerCard answer={answer} model={last.model} live={live && last.streaming} /> : <AnswerBody answer={answer} live={live && last.streaming} />)}
      {last.stopReason === "max_tokens" && <div className="text-xs text-warn">{t("输出达到 max_tokens 上限，被截断。")}</div>}
      {last.stopReason === "refusal" && <div className="text-xs text-danger">{t("厂商拒绝了这次请求。")}</div>}
    </div>
  );
}

type AnswerBlocks = AssistantItem["blocks"];

function AnswerBody({ answer, live }: { answer: AnswerBlocks; live: boolean }) {
  return (
    <>
      {answer.map((b, i) =>
        b.type === "text" ? (
          <Markdown key={i} text={b.text} streaming={live && i === answer.length - 1} />
        ) : b.type === "image" ? (
          <ImageBlock key={i} mediaType={b.mediaType} data={b.data} url={b.url} />
        ) : null,
      )}
    </>
  );
}

// 展开过程区时，最终回答单独放进一张白色卡片，标题行写明"最终回答"：
// 过程区是浅色底的"草稿"，这里才是真正给用户的答案，两者不会再混在一起。
function AnswerCard({ answer, model, live }: { answer: AnswerBlocks; model: string; live: boolean }) {
  const [copied, setCopied] = useState(false);
  const text = answer.flatMap((b) => (b.type === "text" ? [b.text] : [])).join("\n\n");
  return (
    <section className="card space-y-2 p-4" aria-label={t("最终回答")}>
      <header className="flex items-center gap-2.5">
        <span className="text-accent" aria-hidden="true">◆</span>
        <h3 className="text-[15px] font-semibold">{t("最终回答")}</h3>
        <div className="flex-1" />
        <span className="truncate text-[11px] text-muted">{model}</span>
        {text && (
          <button
            className="rounded-ctl px-2 py-1 text-xs text-muted hover:bg-panel-2 hover:text-fg"
            onClick={async () => {
              if (await copyText(text)) {
                setCopied(true);
                setTimeout(() => setCopied(false), 1500);
              }
            }}
            aria-label={t("复制回答")}
          >
            {copied ? t("已复制") : t("复制")}
          </button>
        )}
      </header>
      <AnswerBody answer={answer} live={live} />
    </section>
  );
}
