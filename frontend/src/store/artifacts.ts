import type { TimelineItem, ToolView } from "./reducer";

// 产物：这个会话里 agent 写出来的文件。
//
// 来源是 write_file / edit_file / move_file 这三个工具的调用（它们的参数里有明确的路径），
// 所以只覆盖"通过工具写出的文件"；agent 用 shell 命令生成的文件看不到，需要的话让它再用 write_file 写一份。
// 恢复历史会话时工具调用会重放，所以产物列表不需要单独保存。

export type ArtifactKind = "html" | "svg" | "mermaid" | "dot" | "markdown" | "image" | "text" | "other";

export interface Artifact {
  /** 工具参数里的路径（相对工作区或绝对路径），同一个文件只出现一次 */
  path: string;
  kind: ArtifactKind;
  /** 最近一次写它的工具和调用 ID（界面据此排序、判断是不是新写的） */
  tool: string;
  callId: string;
  /** 已经成功写出（失败或被拒绝的调用不算产物） */
  status: ToolView["status"];
}

const KIND_BY_EXT: Record<string, ArtifactKind> = {
  html: "html", htm: "html", svg: "svg", mmd: "mermaid", mermaid: "mermaid", dot: "dot", gv: "dot",
  md: "markdown", markdown: "markdown",
  png: "image", jpg: "image", jpeg: "image", gif: "image", webp: "image", bmp: "image", ico: "image",
  txt: "text", json: "text", csv: "text",
};

export function kindOf(path: string): ArtifactKind {
  const ext = path.toLowerCase().split(/[?#]/)[0].split(".").pop() ?? "";
  return KIND_BY_EXT[ext] ?? "other";
}

/** 能在面板里预览（而不只是"打开"）的类型 */
export const previewable = (k: ArtifactKind) => k !== "other";

/** 能用系统默认程序打开的类型（与后端 internal/app/files.go 的白名单一致：不含可执行类型） */
export const OPENABLE_EXT = /\.(html?|svg|md|markdown|txt|mmd|mermaid|dot|gv|json|csv|pdf|png|jpe?g|gif|webp|bmp|ico)$/i;

function pathsOf(tool: ToolView): string[] {
  const o = (tool.input && typeof tool.input === "object" ? tool.input : {}) as Record<string, unknown>;
  const str = (v: unknown) => (typeof v === "string" && v ? [v] : []);
  switch (tool.name) {
    case "write_file":
    case "edit_file":
      return str(o.path);
    case "move_file":
      return str(o.destination);
    default:
      return [];
  }
}

/** 按时间线顺序收集本会话的产物；同一个文件多次写入只保留最近一次，最新写的排在最前面。 */
export function collectArtifacts(timeline: TimelineItem[], tools: Record<string, ToolView>): Artifact[] {
  const byPath = new Map<string, Artifact>();
  for (const item of timeline) {
    if (item.kind !== "assistant") continue;
    for (const b of item.blocks) {
      if (b.type !== "tool") continue;
      const tool = tools[b.callId];
      if (!tool || tool.status !== "done") continue;
      for (const path of pathsOf(tool)) {
        byPath.delete(path); // 重新插入，让 Map 的顺序 = 最近一次写入的顺序
        byPath.set(path, { path, kind: kindOf(path), tool: tool.name, callId: tool.callId, status: tool.status });
      }
    }
  }
  return [...byPath.values()].reverse();
}

const isAbs = (p: string) => p.startsWith("/") || /^[a-zA-Z]:[\\/]/.test(p) || p.startsWith("\\\\");

/**
 * 文件在电脑上的绝对路径（悬停时显示）。路径分隔符跟工作区保持一致（Windows 工作区用反斜杠）。
 * 只是显示用的拼接，不做符号链接解析；真正打开时由后端解析并校验。
 */
export function absPath(workspace: string, p: string): string {
  const clean = p.replace(/^file:\/\//, "").replace(/^\.\//, "");
  if (isAbs(clean) || !workspace) return clean;
  const win = workspace.includes("\\") && !workspace.includes("/");
  const sep = win ? "\\" : "/";
  const parts: string[] = [];
  for (const seg of clean.split(/[\\/]+/)) {
    if (!seg || seg === ".") continue;
    if (seg === "..") parts.pop();
    else parts.push(seg);
  }
  return workspace.replace(/[\\/]+$/, "") + sep + parts.join(sep);
}

/** 聊天正文里的行内代码是不是一个文件路径（`report.html`、`out/flow.svg`）：有可打开的扩展名、没有空格和 URL。 */
export function looksLikeFilePath(text: string): boolean {
  if (text.length > 260 || /\s/.test(text) || /^[a-z][a-z0-9+.-]*:\/\//i.test(text) || /[<>"|?*]/.test(text)) return false;
  return OPENABLE_EXT.test(text);
}
