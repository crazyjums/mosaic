import { useState } from "react";
import { t } from "../i18n";

// 用户发出的消息气泡。粘贴一大段日志 / 代码时，整段堆在对话里会把后面的内容挤得很远，
// 所以超过阈值（行数或字数）就默认折叠，只显示开头，点击展开全文；文字仍可选中复制。
export const COLLAPSE_LINES = 10;
export const COLLAPSE_CHARS = 800;

/** 是否需要折叠：行数或字数超过阈值。 */
export const shouldCollapse = (text: string): boolean => text.length > COLLAPSE_CHARS || text.split("\n").length > COLLAPSE_LINES;

export default function UserBubble({ text }: { text: string }) {
  const long = shouldCollapse(text);
  const [open, setOpen] = useState(false);
  const collapsed = long && !open;
  const lines = text.split("\n").length;

  return (
    <div className="max-w-[80%] rounded-2xl rounded-br-sm bg-accent-soft px-4 py-2.5 text-[15px] leading-relaxed">
      <div className={`relative whitespace-pre-wrap break-words ${collapsed ? "max-h-[13.5rem] overflow-hidden" : ""}`}>
        {text}
        {/* 折叠时底部渐隐，提示后面还有内容 */}
        {collapsed && <div className="pointer-events-none absolute inset-x-0 bottom-0 h-10 bg-gradient-to-t from-[var(--accent-soft)] to-transparent" aria-hidden="true" />}
      </div>
      {long && (
        <button className="mt-1.5 text-xs font-medium text-accent hover:underline focus-visible:outline-2 focus-visible:outline-accent" onClick={() => setOpen(!open)} aria-expanded={open}>
          {open ? t("收起") + " ▴" : t("展开全文 · 共 {n} 行 / {c} 字", { n: lines, c: text.length }) + " ▾"}
        </button>
      )}
    </div>
  );
}
