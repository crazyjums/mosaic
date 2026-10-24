import { useEffect, useRef } from "react";
import { useChat } from "../store/chatStore";
import AssistantMessage from "./AssistantMessage";

const REASON: Record<string, string> = {
  completed: "完成",
  max_steps: "达到最大步数",
  cancelled: "已取消",
  error: "出错",
};

// 消息流。自动滚动策略：只有用户本来就在底部附近时才跟随滚动，
// 否则用户往上翻看历史时会被新内容不停地拽回底部。
export default function MessageList() {
  const timeline = useChat((s) => s.timeline);
  const backendKind = useChat((s) => s.backendKind);
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  useEffect(() => {
    const el = ref.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [timeline]);

  const onScroll = () => {
    const el = ref.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  };

  return (
    <div ref={ref} onScroll={onScroll} className="min-w-0 flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-5 px-6 py-6">
        {timeline.length === 0 && <Empty demo={backendKind === "demo"} />}
        {timeline.map((it) => {
          switch (it.kind) {
            case "user":
              return (
                <div key={it.id} className="flex justify-end">
                  <div className="max-w-[80%] whitespace-pre-wrap rounded-2xl rounded-br-sm bg-accent/15 px-4 py-2 text-[15px]">{it.text}</div>
                </div>
              );
            case "assistant":
              return <AssistantMessage key={it.id} item={it} />;
            case "error":
              return (
                <div key={it.id} className={`rounded-lg border px-3 py-2 text-sm ${it.fatal ? "border-danger/40 bg-danger/10 text-danger" : "border-warn/40 bg-warn/10 text-warn"}`}>
                  <span className="mr-2 font-mono text-xs opacity-70">{it.code}</span>
                  {it.message}
                </div>
              );
            case "notice":
              return (
                <div key={it.id} className="text-center text-xs text-muted">
                  {it.text}
                </div>
              );
            case "turn_end":
              if (it.replay) return null;
              return (
                <div key={it.id} className="flex items-center gap-3 text-[11px] text-muted">
                  <div className="h-px flex-1 bg-line" />
                  {REASON[it.reason] ?? it.reason} · {it.steps} 步 · 入 {it.usage.input_tokens} / 出 {it.usage.output_tokens} tokens
                  <div className="h-px flex-1 bg-line" />
                </div>
              );
          }
        })}
      </div>
    </div>
  );
}

function Empty({ demo }: { demo: boolean }) {
  return (
    <div className="mt-24 text-center text-muted">
      <div className="text-2xl font-semibold text-fg">myllmapp</div>
      <p className="mt-2 text-sm">自己实现的 agent harness · 思考、工具调用、权限确认全过程可视化</p>
      {demo && (
        <p className="mx-auto mt-6 max-w-md rounded-lg border border-line bg-panel px-4 py-3 text-xs">
          当前是<strong className="text-fg">浏览器演示模式</strong>：没有连接 Go 后端，回放的是 fake provider 录制的事件流。
          随便输入一句话试试；在 Wails 桌面端中会连接真实内核。
        </p>
      )}
    </div>
  );
}
