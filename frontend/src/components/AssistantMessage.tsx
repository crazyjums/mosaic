import type { AssistantItem } from "../store/reducer";
import { useChat } from "../store/chatStore";
import ImageBlock from "./ImageBlock";
import Markdown from "./Markdown";
import ThinkingCard from "./ThinkingCard";
import ToolCallCard from "./ToolCallCard";

// 一条 assistant 消息 = 一次模型调用（一个 step）。按块的顺序渲染：思考、正文、图片、工具调用。
export default function AssistantMessage({ item }: { item: AssistantItem }) {
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
            return <Markdown key={i} text={b.text} streaming={live} />;
          case "image":
            return <ImageBlock key={i} mediaType={b.mediaType} data={b.data} url={b.url} />;
          case "tool": {
            const t = tools[b.callId];
            return t ? <ToolCallCard key={b.callId} tool={t} /> : null;
          }
        }
      })}
      {item.streaming && item.blocks.length === 0 && <div className="caret text-sm text-muted">等待模型响应</div>}
      {item.stopReason === "max_tokens" && <div className="text-xs text-warn">输出达到 max_tokens 上限，被截断。</div>}
      {item.stopReason === "refusal" && <div className="text-xs text-danger">厂商拒绝了这次请求。</div>}
    </div>
  );
}
