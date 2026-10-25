import { Children, isValidElement, useState, type ReactNode } from "react";
import ReactMarkdown, { defaultUrlTransform, type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { backend } from "../bridge";
import { t } from "../i18n";
import { IMAGE_EXT, isLocalPath, MermaidDiagram, normalizeLocalPath, SvgBlock, WorkspaceImage, ZoomableImage } from "./MediaView";

// 模型输出的正文按 Markdown 渲染（支持表格、任务列表等 GFM 语法）。
// react-markdown 默认不渲染原始 HTML，模型输出里的 <script> 之类不会被执行。
//
// 在默认渲染之上补充了图片能力：
//   - ![](效果图.svg) 和指向工作区图片的链接 [效果图](效果图.svg)：从工作区读出图片显示在原处；
//   - ```mermaid 代码块渲染成图，```svg 代码块显示成图片（流式输出期间先显示源码，避免反复渲染半截内容）；
//   - 链接：网址在系统浏览器中打开，本地路径不跳转（否则 WebView 会离开应用页面）。

const codeText = (children: ReactNode): string =>
  Children.toArray(children)
    .map((c) => (typeof c === "string" ? c : ""))
    .join("");

function makeComponents(streaming: boolean): Components {
  return {
    img: ({ src, alt }) => {
      const s = typeof src === "string" ? src : "";
      if (!s) return null;
      if (/^data:image\//.test(s) || /^https?:\/\//.test(s)) return <ZoomableImage src={s} alt={alt} />;
      if (isLocalPath(s)) return <WorkspaceImage path={normalizeLocalPath(s)} alt={alt} />;
      return null;
    },
    a: ({ href, children }) => {
      const h = href ?? "";
      const local = isLocalPath(h);
      const path = local ? normalizeLocalPath(h) : "";
      return (
        <>
          <a
            href={h}
            title={local ? path : h}
            onClick={(e) => {
              if (h.startsWith("#")) return;
              e.preventDefault();
              if (/^https?:\/\//.test(h)) void backend().openExternalURL(h);
            }}
          >
            {children}
          </a>
          {/* 链接指向工作区里的图片：在链接下方直接显示出来 */}
          {local && IMAGE_EXT.test(path) && !streaming && <WorkspaceImage path={path} alt={path} />}
        </>
      );
    },
    // 表格放进一个可横向滚动的圆角外框：窄窗口里不撑破版面，宽度仍然撑满正文
    table: ({ children }) => (
      <div className="md-table">
        <table>{children}</table>
      </div>
    ),
    pre: ({ children }) => {
      // 代码块是 <pre><code class="language-x">…</code></pre>；图类代码块整个替换掉 <pre>。
      const child = Children.toArray(children)[0];
      if (!streaming && isValidElement<{ className?: string; children?: ReactNode }>(child)) {
        const lang = /language-([\w-]+)/.exec(child.props.className ?? "")?.[1]?.toLowerCase();
        const code = codeText(child.props.children).replace(/\n$/, "");
        if (lang === "mermaid") return <MermaidDiagram code={code} />;
        if (lang === "svg" || ((lang === "xml" || lang === "html") && /^\s*(<\?xml[^>]*>\s*)?<svg[\s>]/i.test(code))) return <SvgBlock code={code} />;
      }
      // 普通代码块：顶部一条语言标签 + 复制按钮。流式输出期间也显示，用户可以复制已经出来的部分。
      const lang = isValidElement<{ className?: string }>(child) ? /language-([\w-]+)/.exec(child.props.className ?? "")?.[1] : undefined;
      const text = isValidElement<{ children?: ReactNode }>(child) ? codeText(child.props.children).replace(/\n$/, "") : "";
      return <CodeBlock lang={lang} text={text}>{children}</CodeBlock>;
    },
  };
}

// 默认的 URL 过滤会去掉 C:\... 这样的 Windows 绝对路径（看起来像一个未知协议），这里放行它们；
// 其余仍按默认规则（javascript: 之类会被清掉）。
const urlTransform = (url: string) => (/^[a-zA-Z]:[\\/]/.test(url) ? url : defaultUrlTransform(url));

// 复制到剪贴板：优先用异步剪贴板接口；某些 WebView 里不可用时退回到选中文本再 execCommand。
async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try {
      return document.execCommand("copy");
    } finally {
      ta.remove();
    }
  }
}

function CodeBlock({ lang, text, children }: { lang?: string; text: string; children: ReactNode }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="code-block">
      <div className="code-bar">
        <span>{lang ?? "text"}</span>
        <button
          className="code-copy"
          onClick={async () => {
            if (await copyText(text)) {
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            }
          }}
          aria-label={t("复制代码")}
        >
          {copied ? t("已复制") : t("复制")}
        </button>
      </div>
      <pre>{children}</pre>
    </div>
  );
}

const COMPONENTS = { live: makeComponents(true), done: makeComponents(false) };

export default function Markdown({ text, streaming, size = "base" }: { text: string; streaming?: boolean; size?: "base" | "sm" }) {
  return (
    <div className={`md leading-relaxed ${size === "sm" ? "text-[13px]" : "text-[15px]"} ${streaming ? "caret" : ""}`}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={streaming ? COMPONENTS.live : COMPONENTS.done} urlTransform={urlTransform}>
        {text}
      </ReactMarkdown>
    </div>
  );
}
