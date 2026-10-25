import { useState } from "react";
import type { AttachmentInfo } from "../protocol/events";
import { t } from "../i18n";
import { fmtSize } from "../store/attachments";
import { ZoomableImage } from "./MediaView";

// 用户发出的消息气泡。粘贴一大段日志 / 代码时，整段堆在对话里会把后面的内容挤得很远，
// 所以超过阈值（行数或字数）就默认折叠，只显示开头，点击展开全文；文字仍可选中复制。
export const COLLAPSE_LINES = 10;
export const COLLAPSE_CHARS = 800;

/** 是否需要折叠：行数或字数超过阈值。 */
export const shouldCollapse = (text: string): boolean => text.length > COLLAPSE_CHARS || text.split("\n").length > COLLAPSE_LINES;

export default function UserBubble({ text, attachments = [] }: { text: string; attachments?: AttachmentInfo[] }) {
  const long = shouldCollapse(text);
  const [open, setOpen] = useState(false);
  const collapsed = long && !open;
  const lines = text.split("\n").length;

  return (
    <div className="max-w-[80%] rounded-2xl rounded-br-sm bg-accent-soft px-4 py-2.5 text-[15px] leading-relaxed">
      {attachments.length > 0 && (
        // 附件放在文字上面：图片是缩略图（点击放大），文本文件是文件名 + 大小
        <ul className={`flex flex-wrap gap-2 ${text ? "mb-2" : ""}`} aria-label={t("附件")}>
          {attachments.map((a, i) => (
            <li key={i}>
              {a.kind === "image" && a.data ? (
                <div className="h-20 w-20 overflow-hidden rounded-ctl border border-line bg-panel">
                  <ZoomableImage src={`data:${a.media_type};base64,${a.data}`} alt={a.name} />
                </div>
              ) : (
                <div className="flex max-w-[16rem] items-center gap-2 rounded-ctl border border-line bg-panel px-2.5 py-1.5" title={a.name}>
                  <span aria-hidden="true">{a.kind === "image" ? "🖼" : "📄"}</span>
                  <span className="min-w-0">
                    <span className="block truncate text-xs font-medium">{a.name}</span>
                    <span className="block text-[10px] text-muted">{fmtSize(a.size)}</span>
                  </span>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
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
