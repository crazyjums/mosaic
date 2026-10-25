import type { AssistantItem } from "../store/reducer";
import { useChat } from "../store/chatStore";
import ImageBlock from "./ImageBlock";
import Markdown from "./Markdown";
import ThinkingCard from "./ThinkingCard";
import ToolCallCard from "./ToolCallCard";

// 一条 assistant 消息 = 一次模型调用（一个 step）。按块的顺序渲染：思考、正文、图片、工具调用。
// omitAnswer：这条消息的正文已经作为"最终回答"显示在折叠区外面（见 TurnView），这里不再重复。
export default function AssistantMessage({ item, omitAnswer = false }: { item: AssistantItem; omitAnswer?: boolean }) {
  const tools = useChat((s) => s.tools);
  const lastIndex = item.blocks.length - 1;

  return (
    <div className="space-y-2">
      <div className="text-[11px] uppercase tracking-wide text-muted">
        step {item.step} · {item.provider}/{item.model}
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
