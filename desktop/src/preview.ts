import { randomUUID, timingSafeEqual } from "node:crypto";

// 网页产物的预览（迁移阶段 5 之前的收尾：给应用页面加 CSP）。
//
// 为什么要单独一个协议：产物面板把 AI 生成的 HTML 放进沙箱 iframe。用 srcdoc 的话，iframe 会继承父页面的 CSP，
// 父页面 CSP 一严格（不允许内联脚本），“允许脚本”的预览就全坏了。让预览走自己的来源（mosaic-preview://），
// 不继承应用页面的 CSP，同时靠响应头里的 CSP sandbox 指令把它关在沙箱里——和 Wails 版的隔离强度一样。

export const PREVIEW_SCHEME = "mosaic-preview";

interface Entry {
  html: string;
  scripts: boolean;
}

/** 最近的若干份预览放在内存里：单份有上限，总数有上限，旧的先丢。 */
export class PreviewStore {
  private entries = new Map<string, Entry>();
  constructor(
    private readonly maxEntries = 32,
    private readonly maxBytes = 16 << 20,
  ) {}

  put(html: string, scripts: boolean): string {
    if (Buffer.byteLength(html) > this.maxBytes) throw new Error("网页太大，无法预览");
    const id = randomUUID();
    this.entries.set(id, { html, scripts });
    while (this.entries.size > this.maxEntries) this.entries.delete(this.entries.keys().next().value as string);
    return id;
  }

  get(id: string): Entry | undefined {
    return this.entries.get(id);
  }
}

/** 不允许脚本时：禁止一切联网，只允许内联样式和 data: 图片；永远在沙箱里。 */
const NO_SCRIPT_CSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:";
/** 允许脚本时：仍然在没有 allow-same-origin 的沙箱里（独立的空来源，访问不到应用的任何东西），但可以联网加载图表库。 */
const SCRIPT_CSP = "sandbox allow-scripts";

/**
 * 请求是不是应用页面发的。自定义协议的页面发出的跨源请求，Origin 是字符串 "null"、Referer 也被去掉了（实测），
 * 光看请求头判断不了。所以用一次运行一个的随机令牌：主进程只把它交给应用页面的 preload（校验发送方地址），
 * 预览 iframe 没有 preload，拿不到它，也就存不进新的预览。
 */
export function fromApp(req: Request, token: string): boolean {
  const got = Buffer.from(req.headers.get(TOKEN_HEADER) ?? "");
  const want = Buffer.from(token);
  return token !== "" && got.length === want.length && timingSafeEqual(got, want);
}

export const TOKEN_HEADER = "x-mosaic-token";

/**
 * 处理 mosaic-preview:// 的请求。
 *   POST mosaic-preview://store/?scripts=1   body=HTML（要带令牌，只接受应用页面发来的）→ {"url": "mosaic-preview://view/<id>"}
 *   GET  mosaic-preview://view/<id>          → 那份 HTML
 */
export async function handlePreview(req: Request, store: PreviewStore, appOrigin: string, token: string): Promise<Response> {
  const u = new URL(req.url);
  const cors = { "access-control-allow-origin": appOrigin, vary: "Origin" };
  // 带自定义请求头的跨源 POST 要先过预检。
  if (req.method === "OPTIONS") {
    return new Response(null, { status: 204, headers: { ...cors, "access-control-allow-methods": "POST", "access-control-allow-headers": `${TOKEN_HEADER}, content-type` } });
  }
  if (u.hostname === "store" && req.method === "POST") {
    if (!fromApp(req, token)) return new Response("forbidden", { status: 403, headers: cors });
    try {
      const id = store.put(await req.text(), u.searchParams.get("scripts") === "1");
      return Response.json({ url: `${PREVIEW_SCHEME}://view/${id}` }, { headers: cors });
    } catch (e) {
      return new Response(e instanceof Error ? e.message : "error", { status: 413, headers: cors });
    }
  }
  if (u.hostname === "view" && req.method === "GET") {
    const entry = store.get(u.pathname.replace(/^\//, ""));
    if (!entry) return new Response("not found", { status: 404 });
    return new Response(entry.html, {
      headers: {
        "content-type": "text/html; charset=utf-8",
        "content-security-policy": entry.scripts ? SCRIPT_CSP : NO_SCRIPT_CSP,
        "x-content-type-options": "nosniff",
        "cache-control": "no-store",
      },
    });
  }
  return new Response("not found", { status: 404 });
}
