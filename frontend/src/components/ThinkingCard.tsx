import { useEffect, useState } from "react";
import { t } from "../i18n";
import { useUIPrefs } from "../store/uiPrefs";
import Markdown from "./Markdown";

/** 只有一行加粗小标题：GPT 的思考摘要偶尔就是这样（OpenAI 不公开原始思考过程）。 */
export const isTitleOnly = (text: string) => /^\s*\*\*[^*\n]+\*\*\s*$/.test(text);

// 可折叠的思考卡片。默认展开方式由 设置 → 外观 → 思考过程默认 决定：
//   - 自动：正在流式输出时展开（让用户看到模型在想什么），结束后折叠（不喧宾夺主）；
//   - 总是展开 / 总是折叠。
// 用户手动展开/折叠之后就以用户的选择为准。
export default function ThinkingCard({ text, redacted, streaming }: { text: string; redacted: boolean; streaming: boolean }) {
  const mode = useUIPrefs((s) => s.prefs.thinking);
  const [userOpen, setUserOpen] = useState<boolean | null>(null);
  const open = userOpen ?? (mode === "auto" ? streaming : mode === "expanded");

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
        <span className="font-medium">{streaming ? t("思考中…") : t("思考过程")}</span>
        {!streaming && !redacted && <span className="text-muted">{t("{n} 字", { n: text.length })}</span>}
      </button>
      {open && (
        <div className="px-4 pb-3 text-muted">
          {redacted ? (
            <div className="text-[13px]">{t("（厂商返回了加密的思考内容，无法显示）")}</div>
          ) : (
            // 思考摘要里常有 **小标题** 和列表，按 Markdown 渲染，而不是把星号原样显示出来。
            <Markdown text={text} streaming={streaming} size="sm" />
          )}
          {!streaming && !redacted && isTitleOnly(text) && (
            <div className="mt-1 text-[11px] opacity-80">{t("模型只返回了思考的小标题：OpenAI 不公开完整的思考过程，只提供摘要，摘要有时很短。")}</div>
          )}
        </div>
      )}
    </div>
  );
}
