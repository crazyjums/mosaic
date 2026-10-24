import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// 模型输出的正文按 Markdown 渲染（支持表格、任务列表等 GFM 语法）。
// react-markdown 默认不渲染原始 HTML，模型输出里的 <script> 之类不会被执行。
export default function Markdown({ text, streaming }: { text: string; streaming?: boolean }) {
  return (
    <div className={`md text-[15px] leading-relaxed ${streaming ? "caret" : ""}`}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>
    </div>
  );
}
