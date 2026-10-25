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

  return (
    <div className="space-y-2">
      <div className="text-[11px] uppercase tracking-wide text-muted">
        step {item.step} · {item.provider}/{item.model}
        {item.route && (
          <span className="ml-2 normal-case text-muted" title={t("模型组「{g}」选中的成员", { g: item.route.group })}>
            [{item.route.group}]
          </span>
        )}
        {item.route && item.route.handedOn.length > 0 && <HandoffMark route={item.route} />}
        {item.agentId !== "main" && <span className="ml-2 text-accent">agent {item.agentId}</span>}
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
            const t = tools[b.callId];
            return t ? <ToolCallCard key={b.callId} tool={t} /> : null;
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
