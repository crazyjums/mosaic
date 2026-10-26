import { useEffect, useState } from "react";
import { backend } from "../bridge";
import { t } from "../i18n";

// 聊天里的图片：工作区里的图片文件、```svg 代码块、```mermaid 图。
//
// 所有图片都以 <img src="data:..."> 显示，而不是把 SVG 直接插进页面：<img> 里的 SVG 不会执行脚本、
// 也不能加载外部资源，模型生成的 SVG 里即使有 <script> 也是无害的。点击图片放大查看。

/** 能在聊天里预览的图片扩展名（与后端 internal/app/image.go 一致）。 */
export const IMAGE_EXT = /\.(svg|png|jpe?g|gif|webp|bmp|ico)$/i;

/** 是否是工作区里的本地路径（而不是网址、锚点、data URL）。 */
export function isLocalPath(href: string): boolean {
  return !!href && !/^[a-z][a-z0-9+.-]*:/i.test(href.replace(/^[a-zA-Z]:[\\/]/, "/")) && !href.startsWith("#") && !href.startsWith("//");
}

/** Markdown 里的路径可能是 URL 编码的（中文文件名）或带 ./ 前缀。 */
export function normalizeLocalPath(href: string): string {
  let p = href.replace(/^file:\/\//, "");
  try {
    p = decodeURI(p);
  } catch {
    // 不是合法的编码就原样使用
  }
  return p.replace(/^\.\//, "").split(/[?#]/)[0];
}

export function svgDataURL(svg: string): string {
  return "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
}

/** 可以点击放大的图片。 */
export function ZoomableImage({ src, alt }: { src: string; alt?: string }) {
  const [zoom, setZoom] = useState(false);
  useEffect(() => {
    if (!zoom) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setZoom(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [zoom]);
  return (
    <>
      <img
        src={src}
        alt={alt ?? ""}
        className="my-1 max-h-96 max-w-full cursor-zoom-in rounded-md border border-line bg-white"
        onClick={() => setZoom(true)}
      />
      {zoom && (
        <div className="fixed inset-0 z-50 flex cursor-zoom-out items-center justify-center bg-black/70 p-6" onClick={() => setZoom(false)} role="dialog" aria-label={alt || t("图片")}>
          <img src={src} alt={alt ?? ""} className="max-h-full max-w-full rounded-md bg-white shadow-2xl" />
        </div>
      )}
    </>
  );
}

/** 工作区里的图片：经后端读成 data URL。读不到时显示原因（例如文件还没写出来）。 */
export function WorkspaceImage({ path, alt }: { path: string; alt?: string }) {
  const [state, setState] = useState<{ src?: string; error?: string }>({});
  useEffect(() => {
    let alive = true;
    setState({});
    backend()
      .readWorkspaceImage(path)
      .then((src) => alive && setState({ src }))
      .catch((e) => alive && setState({ error: String(e instanceof Error ? e.message : e) }));
    return () => {
      alive = false;
    };
  }, [path]);
  if (state.src) return <ZoomableImage src={state.src} alt={alt || path} />;
  if (state.error) return <span className="block text-xs text-muted">🖼 {path}：{state.error}</span>;
  return <span className="block text-xs text-muted">🖼 {t("加载图片…")}</span>;
}

// mermaid.render 不能并发调用（共享内部状态，同时渲染两张图会报 "Cannot read properties of null"），
// 所以串成一个队列。主题固定用浅色：图放在白底的 <img> 里，深色主题的黑色方块在白底上反而看不清。
let mermaidQueue: Promise<unknown> = Promise.resolve();
let mermaidReady = false;
let mermaidSeq = 0;
function renderMermaid(code: string): Promise<string> {
  const job = mermaidQueue.then(async () => {
    const mermaid = (await import("mermaid")).default;
    if (!mermaidReady) {
      // strict：不允许图里的点击回调和 HTML；htmlLabels 关掉，标签用纯 SVG 文本，放进 <img> 里也能正常显示。
      mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: "default", flowchart: { htmlLabels: false } });
      mermaidReady = true;
    }
    const { svg } = await mermaid.render(`mmd-${++mermaidSeq}`, code);
    return svg;
  });
  mermaidQueue = job.catch(() => {});
  return job;
}

// Graphviz（DOT 语言）：用 @viz-js/viz（Graphviz 编译成 WebAssembly），在本地渲染，不联网。体积不小，只在第一次用到时才加载。
let vizPromise: Promise<{ renderString(src: string, opts?: { format?: string }): string }> | null = null;
async function renderDot(code: string): Promise<string> {
  vizPromise ??= import("@viz-js/viz").then((m) => m.instance());
  return (await vizPromise).renderString(code, { format: "svg" });
}

/** 把一段图的源码渲染成 SVG 并显示；失败时显示源码和错误，可以切换查看源码。 */
function RenderedDiagram({ title, code, render }: { title: string; code: string; render: (code: string) => Promise<string> }) {
  const [state, setState] = useState<{ svg?: string; error?: string }>({});
  const [showSource, setShowSource] = useState(false);
  useEffect(() => {
    let alive = true;
    setState({});
    render(code)
      .then((svg) => alive && setState({ svg }))
      .catch((e) => alive && setState({ error: String(e instanceof Error ? e.message : e) }));
    return () => {
      alive = false;
    };
  }, [code, render]);
  return (
    <div className="not-prose my-2 rounded-lg border border-line bg-panel px-3 py-2">
      <div className="mb-1 flex text-[11px] text-muted">
        <span>{title}</span>
        <div className="flex-1" />
        <button className="hover:text-fg" onClick={() => setShowSource(!showSource)}>
          {showSource ? t("显示图") : t("显示源码")}
        </button>
      </div>
      {state.error && <div className="mb-1 text-xs text-danger">{t("图渲染失败：{err}", { err: state.error.split("\n")[0] })}</div>}
      {showSource || state.error ? (
        <pre className="overflow-x-auto text-xs">
          <code>{code}</code>
        </pre>
      ) : state.svg ? (
        <ZoomableImage src={svgDataURL(state.svg)} alt={title} />
      ) : (
        <div className="text-xs text-muted">{t("正在渲染图…")}</div>
      )}
    </div>
  );
}

/** ```mermaid 代码块：按需加载 mermaid（体积大，只在第一次用到时下载），渲染失败时显示源码和错误。 */
export function MermaidDiagram({ code }: { code: string }) {
  return <RenderedDiagram title={t("Mermaid 图")} code={code} render={renderMermaid} />;
}

/** ```dot / ```graphviz 代码块：Graphviz 图（流程图、依赖图、状态机等）。 */
export function GraphvizDiagram({ code }: { code: string }) {
  return <RenderedDiagram title={t("Graphviz 图")} code={code} render={renderDot} />;
}

/** ```svg 代码块（或内容是 <svg> 的 xml/html 代码块）：显示图，可以切换到源码。 */
export function SvgBlock({ code }: { code: string }) {
  const [showSource, setShowSource] = useState(false);
  return (
    <div className="my-2 rounded-lg border border-line bg-panel px-3 py-2">
      <div className="mb-1 flex text-[11px] text-muted">
        <span>SVG</span>
        <div className="flex-1" />
        <button className="hover:text-fg" onClick={() => setShowSource(!showSource)}>
          {showSource ? t("显示图") : t("显示源码")}
        </button>
      </div>
      {showSource ? (
        <pre className="overflow-x-auto text-xs">
          <code>{code}</code>
        </pre>
      ) : (
        <ZoomableImage src={svgDataURL(code)} alt="SVG" />
      )}
    </div>
  );
}
