import { useEffect, useMemo, useState } from "react";
import { backend } from "../bridge";
import { t } from "../i18n";
import { absPath, collectArtifacts, OPENABLE_EXT, type Artifact, type ArtifactKind } from "../store/artifacts";
import { useChat } from "../store/chatStore";
import { FileLink, openFileWithToast } from "./FileLink";
import Markdown, { copyText } from "./Markdown";
import { GraphvizDiagram, MermaidDiagram, svgDataURL, WorkspaceImage, ZoomableImage } from "./MediaView";

// 产物面板：这个会话里 agent 写出来的文件，点一个就在下面预览——
// 网页（沙箱 iframe）、SVG、Mermaid / Graphviz 流程图、Markdown、图片、文本。
// 数据来自工具调用（store/artifacts.ts），文件内容经后端读取（只能读工作区内的白名单类型）。

const ICON: Record<ArtifactKind, string> = { html: "🌐", svg: "🖼", mermaid: "🧜", dot: "🔀", markdown: "📝", image: "🖼", text: "📄", other: "📦" };
const KIND_LABEL: Record<ArtifactKind, string> = { html: "网页", svg: "SVG", mermaid: "Mermaid", dot: "Graphviz", markdown: "Markdown", image: "图片", text: "文本", other: "文件" };

const baseName = (p: string) => p.split(/[\\/]/).filter(Boolean).pop() ?? p;
const dirName = (p: string) => {
  const parts = p.split(/[\\/]/).filter(Boolean);
  return parts.length > 1 ? parts.slice(0, -1).join("/") : "";
};

export default function ArtifactsPanel() {
  const timeline = useChat((s) => s.timeline);
  const tools = useChat((s) => s.tools);
  const sessionId = useChat((s) => s.sessionId);
  const workspace = useChat((s) => s.workspace);
  const list = useMemo(() => collectArtifacts(timeline, tools), [timeline, tools]);
  // 用户点过哪个就看哪个；没点过时跟着最新写出的文件走
  const [picked, setPicked] = useState<string | null>(null);
  useEffect(() => setPicked(null), [sessionId]);
  const current = list.find((a) => a.path === picked) ?? list[0];

  if (list.length === 0) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center text-xs text-muted">
        <span className="text-2xl" aria-hidden="true">📦</span>
        <div>{t("这个会话还没有产物")}</div>
        <div>{t("agent 用 write_file / edit_file 写出的网页、流程图、文档会出现在这里，可以直接预览。")}</div>
      </div>
    );
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ul className="max-h-[34%] shrink-0 overflow-y-auto border-b border-line p-2" aria-label={t("产物列表")}>
        {list.map((a) => (
          <li key={a.path}>
            <button
              className={`flex w-full items-center gap-2 rounded-ctl px-2 py-1.5 text-left text-xs hover:bg-accent-soft ${a.path === current?.path ? "bg-accent-soft" : ""}`}
              onClick={() => setPicked(a.path)}
              title={absPath(workspace, a.path)}
            >
              <span aria-hidden="true">{ICON[a.kind]}</span>
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">{baseName(a.path)}</span>
                {dirName(a.path) && <span className="block truncate text-[10px] text-muted">{dirName(a.path)}</span>}
              </span>
              <span className="shrink-0 rounded-full bg-panel-2 px-1.5 text-[10px] text-muted">{t(KIND_LABEL[a.kind])}</span>
            </button>
          </li>
        ))}
      </ul>
      {current && <ArtifactViewer key={current.path} artifact={current} />}
    </div>
  );
}

/** 一个产物的预览：顶部是路径和操作按钮，下面是内容。放大时（big）占满弹窗。 */
export function ArtifactViewer({ artifact, big = false, onZoom }: { artifact: Artifact; big?: boolean; onZoom?: () => void }) {
  const workspace = useChat((s) => s.workspace);
  const abs = absPath(workspace, artifact.path);
  const [showSource, setShowSource] = useState(false);
  const [scripts, setScripts] = useState(false);
  const [copied, setCopied] = useState(false);
  const text = useArtifactText(artifact);
  const canToggleSource = ["html", "svg", "mermaid", "dot", "markdown"].includes(artifact.kind);
  const canOpen = OPENABLE_EXT.test(artifact.path);

  return (
    <div className={`flex min-h-0 flex-1 flex-col ${big ? "" : "p-2"}`}>
      <div className="mb-1.5 flex items-center gap-1 text-[11px]">
        <span className="min-w-0 flex-1 truncate font-mono text-muted" title={abs}>{abs}</span>
        {canToggleSource && (
          <button className="rounded-ctl px-1.5 py-0.5 text-muted hover:bg-panel-2 hover:text-fg" onClick={() => setShowSource(!showSource)}>
            {showSource ? t("预览") : t("源码")}
          </button>
        )}
        <button
          className="rounded-ctl px-1.5 py-0.5 text-muted hover:bg-panel-2 hover:text-fg"
          onClick={async () => {
            if (await copyText(abs)) {
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            }
          }}
          title={t("复制绝对路径")}
        >
          {copied ? t("已复制") : t("复制路径")}
        </button>
        {canOpen && (
          <button className="rounded-ctl bg-accent px-2 py-0.5 text-on-accent hover:bg-accent-hover" onClick={() => void openFileWithToast(artifact.path)} title={t("点击用默认程序打开（网页会在默认浏览器里打开）")}>
            {artifact.kind === "html" ? t("在浏览器打开") : t("打开")}
          </button>
        )}
        {onZoom && (
          <button className="rounded-ctl px-1.5 py-0.5 text-muted hover:bg-panel-2 hover:text-fg" onClick={onZoom} title={t("放大预览")} aria-label={t("放大预览")}>
            ⤢
          </button>
        )}
      </div>
      {artifact.kind === "html" && !showSource && (
        <label className="mb-1.5 flex items-center gap-1.5 text-[11px] text-muted" title={t("默认不运行网页里的脚本、也不联网。需要交互图表时再开启；脚本仍然关在沙箱里，访问不到本应用和你的文件。")}>
          <input type="checkbox" checked={scripts} onChange={(e) => setScripts(e.target.checked)} />
          {t("允许运行网页里的脚本（可联网加载图表库）")}
        </label>
      )}
      <div className={`min-h-0 flex-1 overflow-auto rounded-ctl border border-line bg-panel ${big ? "p-3" : "p-2"}`}>
        <ArtifactBody artifact={artifact} text={text} showSource={showSource} scripts={scripts} />
      </div>
    </div>
  );
}

type TextState = { status: "loading" } | { status: "error"; error: string } | { status: "ok"; text: string; truncated: boolean };

/** 读产物的文本内容。重新写入（callId 变了）时重新读。图片不走这里。 */
function useArtifactText(a: Artifact): TextState {
  const [state, setState] = useState<TextState>({ status: "loading" });
  const needsText = a.kind !== "image" && a.kind !== "other";
  useEffect(() => {
    if (!needsText) return;
    let alive = true;
    setState({ status: "loading" });
    backend()
      .readWorkspaceText(a.path)
      .then((f) => alive && setState({ status: "ok", text: f.text, truncated: !!f.truncated }))
      .catch((e) => alive && setState({ status: "error", error: String(e instanceof Error ? e.message : e) }));
    return () => {
      alive = false;
    };
  }, [a.path, a.callId, needsText]);
  return state;
}

function ArtifactBody({ artifact, text, showSource, scripts }: { artifact: Artifact; text: TextState; showSource: boolean; scripts: boolean }) {
  if (artifact.kind === "image") return <WorkspaceImage path={artifact.path} alt={artifact.path} />;
  if (artifact.kind === "other") {
    return (
      <div className="text-xs text-muted">
        {t("这种文件不能在这里预览。")} <FileLink path={artifact.path} />
      </div>
    );
  }
  if (text.status === "loading") return <div className="text-xs text-muted">{t("加载中…")}</div>;
  if (text.status === "error") return <div className="text-xs text-danger">{text.error}</div>;
  const body = (() => {
    if (showSource || artifact.kind === "text") {
      return (
        <pre className="whitespace-pre-wrap break-words font-mono text-xs">
          <code>{text.text}</code>
        </pre>
      );
    }
    switch (artifact.kind) {
      case "html":
        return <HtmlPreview html={text.text} scripts={scripts} />;
      case "svg":
        return <ZoomableImage src={svgDataURL(text.text)} alt={artifact.path} />;
      case "mermaid":
        return <MermaidDiagram code={text.text} />;
      case "dot":
        return <GraphvizDiagram code={text.text} />;
      case "markdown":
        return <Markdown text={text.text} />;
    }
  })();
  return (
    <>
      {body}
      {text.truncated && <div className="mt-2 text-xs text-warn">{t("文件太大，只显示了前面一部分。")}</div>}
    </>
  );
}

// 网页预览放在沙箱 iframe 里（srcdoc）：
//   - 没有 allow-same-origin：网页处于独立的"空"源，访问不到本应用的任何数据、localStorage 和 Wails 接口；
//   - 默认不允许脚本，并用 CSP 禁止一切联网（只允许内联样式和 data: 图片）：纯静态报告可以放心看；
//   - 用户打开"允许脚本"后才加 allow-scripts（去掉 CSP，图表库可以从 CDN 加载），脚本仍然关在沙箱里。
function HtmlPreview({ html, scripts }: { html: string; scripts: boolean }) {
  const doc = useMemo(() => {
    if (scripts) return html;
    const csp = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:">`;
    return csp + html;
  }, [html, scripts]);
  return (
    <iframe
      title={t("网页预览")}
      className="h-full min-h-[18rem] w-full rounded-sm border-0 bg-white"
      sandbox={scripts ? "allow-scripts" : ""}
      srcDoc={doc}
    />
  );
}
