import { useState } from "react";
import type { ToolStatus, ToolView } from "../store/reducer";
import { t } from "../i18n";
import { IMAGE_EXT, svgDataURL, WorkspaceImage, ZoomableImage } from "./MediaView";

// label 是中文原文，渲染时翻译。
const STATUS: Record<ToolStatus, { label: string; className: string }> = {
  queued: { label: "排队中", className: "text-muted" },
  awaiting_permission: { label: "等待确认", className: "text-warn" },
  running: { label: "执行中", className: "text-accent" },
  done: { label: "完成", className: "text-ok" },
  error: { label: "失败", className: "text-danger" },
  denied: { label: "已拒绝", className: "text-danger" },
};

const RISK: Record<string, string> = { read: "只读", write: "写入", exec: "执行" };

/** 从参数里挑出最能代表这次调用的一项，显示在卡片标题上。 */
function headline(input: unknown): string {
  if (input && typeof input === "object") {
    const o = input as Record<string, unknown>;
    for (const k of ["command", "path", "pattern", "source", "query", "url"]) {
      if (typeof o[k] === "string") return o[k] as string;
    }
  }
  return "";
}

// 工具调用卡片：标题行显示工具名、关键参数和状态；展开后显示完整参数和结果。
export default function ToolCallCard({ tool }: { tool: ToolView }) {
  const [open, setOpen] = useState(false);
  const st = STATUS[tool.status];
  const busy = tool.status === "running" || tool.status === "queued";

  return (
    <div className="overflow-hidden rounded-lg border border-line bg-panel">
      <button className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-panel-2" onClick={() => setOpen(!open)}>
        <span className={`text-xs transition-transform ${open ? "rotate-90" : ""}`}>▶</span>
        <span className="font-mono text-[13px] font-semibold">{tool.name}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted">{headline(tool.input)}</span>
        {tool.risk && tool.risk !== "read" && (
          <span className="rounded border border-warn/40 px-1.5 text-[11px] text-warn">{RISK[tool.risk] ? t(RISK[tool.risk]) : tool.risk}</span>
        )}
        <span className={`flex items-center gap-1 text-xs ${st.className}`}>
          {busy && <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-current" />}
          {t(st.label)}
          {/* 耗时为 0 表示不足 1ms 或未知（重放的历史），都不显示 */}
          {!!tool.durationMs && <span className="text-muted">· {tool.durationMs}ms</span>}
        </span>
      </button>
      <ImagePreview tool={tool} />
      {open && (
        <div className="space-y-2 border-t border-line px-3 py-2 text-xs">
          {tool.permission && (
            <div className="text-muted">
              {t("权限：{summary}", { summary: tool.permission.summary })}
              {tool.permission.decision && ` → ${tool.permission.decision}`}
            </div>
          )}
          <Section title={t("参数")} body={JSON.stringify(tool.input, null, 2)} />
          {tool.output !== undefined && (
            <Section title={t("结果")} body={tool.output} error={tool.status === "error" || tool.status === "denied"} />
          )}
        </div>
      )}
    </div>
  );
}

// 写出的是图片（模型常用 write_file 生成 SVG 效果图）：在卡片下方直接显示，不用展开。
// 覆盖写入的 SVG 直接用参数里的内容（历史记录里看到的是当时写的版本）；其他情况从工作区读当前文件。
function ImagePreview({ tool }: { tool: ToolView }) {
  if (tool.status !== "done" || !["write_file", "edit_file"].includes(tool.name)) return null;
  const o = (tool.input && typeof tool.input === "object" ? tool.input : {}) as Record<string, unknown>;
  const path = typeof o.path === "string" ? o.path : "";
  if (!IMAGE_EXT.test(path)) return null;
  const inline = tool.name === "write_file" && o.mode !== "append" && /\.svg$/i.test(path) && typeof o.content === "string";
  return (
    <div className="border-t border-line px-3 py-2">
      {inline ? <ZoomableImage src={svgDataURL(o.content as string)} alt={path} /> : <WorkspaceImage path={path} alt={path} />}
    </div>
  );
}

function Section({ title, body, error }: { title: string; body: string; error?: boolean }) {
  return (
    <div>
      <div className="mb-1 text-muted">{title}</div>
      <pre className={`max-h-64 overflow-auto whitespace-pre-wrap rounded bg-panel-2 p-2 font-mono ${error ? "text-danger" : ""}`}>{body}</pre>
    </div>
  );
}
