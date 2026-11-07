import { basename, normalize } from "node:path";

// 壳能打开的独立窗口（白名单）。渲染进程只能说“要哪一种、带什么参数”，窗口怎么开、地址是什么都由这里决定：
// 参数逐项校验，再编码进页面地址的查询串（前端据此只渲染对应的视图）。

export interface AuxSpec {
  /** 同一个 key 最多一个窗口，再次打开就是带到前面 */
  key: string;
  title: string;
  width: number;
  height: number;
  /** 以 ? 开头的查询串 */
  query: string;
}

const ARTIFACT_KINDS = ["html", "svg", "mermaid", "dot", "markdown", "image", "text", "other"];
const MAX_PATH = 4096;

function str(v: unknown, what: string): string {
  if (typeof v !== "string" || v === "" || v.length > MAX_PATH || v.includes("\0")) throw new Error(`参数不合法：${what}`);
  return v;
}

export function auxWindowSpec(kind: unknown, params: unknown): AuxSpec {
  if (kind === "git") return { key: "git", title: "Mosaic — Git", width: 1280, height: 800, query: "?window=git" };
  if (kind === "artifact") {
    const p = (params ?? {}) as Record<string, unknown>;
    const path = str(p.path, "path");
    const workspace = str(p.workspace, "workspace");
    const akind = p.kind;
    if (typeof akind !== "string" || !ARTIFACT_KINDS.includes(akind)) throw new Error("参数不合法：kind");
    const q = new URLSearchParams({ window: "artifact", path, workspace, kind: akind });
    // 同一个文件只开一个窗口：按规范化后的路径（去掉 ./ 和 ..；Windows 上不区分大小写）判断，/w/a.md 和 /w/sub/../a.md 是同一个。
    const canon = process.platform === "win32" ? normalize(path).toLowerCase() : normalize(path);
    return { key: `artifact:${canon}`, title: `Mosaic — ${basename(path.replace(/\\/g, "/"))}`, width: 1000, height: 760, query: `?${q.toString()}` };
  }
  throw new Error("不支持的窗口类型");
}
