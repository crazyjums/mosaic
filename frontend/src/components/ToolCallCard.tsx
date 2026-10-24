import { useState } from "react";
import type { ToolStatus, ToolView } from "../store/reducer";

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
    for (const k of ["command", "path", "query", "url"]) {
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
          <span className="rounded border border-warn/40 px-1.5 text-[11px] text-warn">{RISK[tool.risk] ?? tool.risk}</span>
        )}
        <span className={`flex items-center gap-1 text-xs ${st.className}`}>
          {busy && <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-current" />}
          {st.label}
          {tool.durationMs !== undefined && tool.status !== "denied" && <span className="text-muted">· {tool.durationMs}ms</span>}
        </span>
      </button>
      {open && (
        <div className="space-y-2 border-t border-line px-3 py-2 text-xs">
          {tool.permission && (
            <div className="text-muted">
              权限：{tool.permission.summary}
              {tool.permission.decision && ` → ${tool.permission.decision}`}
            </div>
          )}
          <Section title="参数" body={JSON.stringify(tool.input, null, 2)} />
          {tool.output !== undefined && (
            <Section title="结果" body={tool.output} error={tool.status === "error" || tool.status === "denied"} />
          )}
        </div>
      )}
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
