import type { AssistantItem } from "../store/reducer";
import { useChat } from "../store/chatStore";
import ImageBlock from "./ImageBlock";
import Markdown from "./Markdown";
import ThinkingCard from "./ThinkingCard";
import ToolCallCard from "./ToolCallCard";
import { t } from "../i18n";

// 一条 assistant 消息 = 一次模型调用（一个 step）。按块的顺序渲染：思考、正文、图片、工具调用。
// omitAnswer：这条消息的正文已经作为"最终回答"显示在折叠区外面（见 TurnView），这里不再重复。
export default function AssistantMessage({ item, omitAnswer = false }: { item: AssistantItem; omitAnswer?: boolean }) {
  const tools = useChat((s) => s.tools);
  const lastIndex = item.blocks.length - 1;
  const hasTool = item.blocks.some((b) => b.type === "tool");
  const onlyThinking = !hasTool && item.blocks.every((b) => b.type === "thinking");
  const kind = hasTool ? t("调用工具") : onlyThinking ? t("思考过程") : t("回答");
  const time = item.startedAt ? new Date(item.startedAt).toLocaleTimeString([], { hour12: false }) : "";

  // 一步 = 一张小卡片：左边是步骤编号，标题行显示这一步在做什么、用的模型和开始时间。
  // 卡片有自己的底色，和下面的"最终回答"卡片一眼就能分开。
  return (
    <div className="space-y-2 rounded-ctl border border-line bg-panel/90 p-3">
      <div className="flex items-center gap-2.5 text-[11px] text-muted">
        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-accent text-[11px] font-semibold text-on-accent" aria-hidden="true">
          {item.step}
        </span>
        <span className="min-w-0 truncate uppercase tracking-wide">
          step {item.step} · {kind} · {item.provider}/{item.model}
          {item.route && (
            <span className="ml-2 normal-case" title={t("模型组「{g}」选中的成员", { g: item.route.group })}>
              [{item.route.group}]
            </span>
          )}
          {item.agentId !== "main" && <span className="ml-2 normal-case text-accent">agent {item.agentId}</span>}
        </span>
        {item.route && item.route.handedOn.length > 0 && <HandoffMark route={item.route} />}
        <div className="flex-1" />
        {time && <span className="shrink-0 tabular-nums">{time}</span>}
      </div>
      {item.blocks.map((b, i) => {
        const live = item.streaming && i === lastIndex;
        switch (b.type) {
          case "thinking":
            return <ThinkingCard key={i} text={b.text} redacted={b.redacted} streaming={live} />;
          case "text":
            if (omitAnswer) return null;
            return <Markdown key={i} text={b.text} streaming={live} />;
          case "image":
            if (omitAnswer) return null;
            return <ImageBlock key={i} mediaType={b.mediaType} data={b.data} url={b.url} />;
          case "tool": {
            const tv = tools[b.callId];
            return tv ? <ToolCallCard key={b.callId} tool={tv} /> : null;
          }
        }
      })}
    </div>
  );
}

/** 发生过转交时的标记："由 A 转交"，悬停显示每次失败的原因。 */
export function HandoffMark({ route }: { route: NonNullable<AssistantItem["route"]> }) {
  const first = route.handedOn[0];
  const detail = route.handedOn.map((a) => `${a.profile} / ${a.model}：${t(KIND_TEXT[a.kind] ?? a.kind)} — ${a.message}`).join("\n");
  return (
    <span className="ml-2 rounded border border-warn/50 px-1.5 text-[10px] normal-case text-warn" title={detail}>
      {route.handedOn.length === 1 ? t("由 {p} 转交", { p: first.profile }) : t("转交 {n} 次", { n: route.handedOn.length })}
    </span>
  );
}

const KIND_TEXT: Record<string, string> = {
  rate_limit: "限流", server: "服务端错误", auth: "认证失败", network: "网络 / 超时", quota: "额度用完", context: "上下文超长",
};
