import { useEffect, useState } from "react";

// 可折叠的思考卡片。
// 交互约定：正在流式输出时自动展开（让用户看到模型在想什么），结束后自动折叠（不喧宾夺主），
// 用户手动展开/折叠之后就以用户的选择为准。
export default function ThinkingCard({ text, redacted, streaming }: { text: string; redacted: boolean; streaming: boolean }) {
  const [userOpen, setUserOpen] = useState<boolean | null>(null);
  const open = userOpen ?? streaming;

  // 新一轮流式开始时，清掉之前的手动选择。
  useEffect(() => {
    if (streaming) setUserOpen(null);
  }, [streaming]);

  return (
    <div className="rounded-lg border border-think/30 bg-think/5">
      <button
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs text-think"
        onClick={() => setUserOpen(!open)}
      >
        <span className={`transition-transform ${open ? "rotate-90" : ""}`}>▶</span>
        <span className="font-medium">{streaming ? "思考中…" : "思考过程"}</span>
        {!streaming && !redacted && <span className="text-muted">{text.length} 字</span>}
      </button>
      {open && (
        <div className={`whitespace-pre-wrap px-4 pb-3 text-[13px] leading-relaxed text-muted ${streaming ? "caret" : ""}`}>
          {redacted ? "（厂商返回了加密的思考内容，无法显示）" : text}
        </div>
      )}
    </div>
  );
}
