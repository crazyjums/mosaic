// 附件：在输入框里选择 / 粘贴 / 拖进来的文件，发送前的检查和读取。
//
// 目前支持：图片（PNG / JPEG / GIF / WebP）和纯文本类文件（Markdown、日志、代码、配置……）。
// 这里的上限只是为了尽早提示用户；真正的校验在后端内核（core/attachment.go），两边的数字保持一致。
// 以后支持 PDF、Office 文档时，在 classify 里增加类型、在 readAttachment 里增加读取方式即可。

import type { Attachment } from "../bridge";

export const LIMITS = {
  count: 10,
  imageBytes: 5 << 20,
  textBytes: 200 << 10,
  totalTextBytes: 600 << 10,
};

export const IMAGE_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp"];

const TEXT_EXT = new Set(
  ("md markdown mdx txt text log json jsonl yaml yml toml ini cfg conf properties env csv tsv xml html htm css scss less svg " +
    "js mjs cjs jsx ts tsx go py rb rs java kt kts swift c h cc cpp hpp cs php lua sh bash zsh ps1 bat cmd sql graphql proto gradle diff patch tex rst org").split(" "),
);
const TEXT_NAMES = new Set(["readme", "license", "makefile", "dockerfile", "changelog", "authors", "notice", ".gitignore", ".editorconfig", ".env"]);

/** 文件选择框的 accept 属性 */
export const ACCEPT = ["image/*", ...[...TEXT_EXT].map((e) => "." + e), "text/*"].join(",");

export type FileKind = "image" | "text" | "unsupported";

export function classify(name: string, mime: string): FileKind {
  if (IMAGE_TYPES.includes(mime.toLowerCase())) return "image";
  const lower = name.toLowerCase();
  const ext = lower.includes(".") ? lower.slice(lower.lastIndexOf(".") + 1) : "";
  if (ext === "svg") return "text"; // SVG 是文本（浏览器给的类型是 image/svg+xml），当作代码附件
  if (mime.startsWith("image/")) return "unsupported"; // heic、bmp 等：模型不一定认识，明确拒绝而不是悄悄发出去
  if (mime.startsWith("text/") || TEXT_EXT.has(ext) || TEXT_NAMES.has(lower) || mime === "application/json" || mime === "application/xml") return "text";
  return "unsupported";
}

export function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

/** 输入框里待发送的一个附件（含预览）。 */
export interface PendingAttachment {
  id: string;
  name: string;
  kind: "image" | "text";
  size: number;
  attachment: Attachment;
  /** 图片的预览地址（data URL） */
  preview?: string;
}

/** 检查再添加一个附件会不会超限；返回错误文字，没问题返回 null。 */
export function checkAdd(existing: Pick<PendingAttachment, "kind" | "size">[], incoming: Pick<PendingAttachment, "kind" | "size">): string | null {
  if (existing.length >= LIMITS.count) return `最多添加 ${LIMITS.count} 个附件`;
  if (incoming.kind === "text") {
    const total = existing.filter((a) => a.kind === "text").reduce((n, a) => n + a.size, 0) + incoming.size;
    if (total > LIMITS.totalTextBytes) return `文本附件合计不能超过 ${LIMITS.totalTextBytes >> 10} KB`;
  }
  return null;
}

let seq = 0;
const newId = () => `att_${Date.now().toString(36)}_${seq++}`;

function readAsDataURL(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error ?? new Error("读取文件失败"));
    r.readAsDataURL(file);
  });
}

export type ReadResult = { ok: true; item: PendingAttachment } | { ok: false; error: string };

/** 读取一个文件，检查类型和大小。出错时返回说明（带文件名），不抛异常。 */
export async function readAttachment(file: File, name = file.name || "image"): Promise<ReadResult> {
  const kind = classify(name, file.type || "");
  if (kind === "unsupported") return { ok: false, error: `${name}：暂不支持这种文件。目前支持图片（PNG / JPEG / GIF / WebP）和 Markdown / 纯文本类文件` };
  try {
    if (kind === "image") {
      if (file.size > LIMITS.imageBytes) return { ok: false, error: `${name}：图片太大（${fmtSize(file.size)}，上限 ${LIMITS.imageBytes >> 20} MB）` };
      const url = await readAsDataURL(file);
      const data = url.slice(url.indexOf(",") + 1);
      return { ok: true, item: { id: newId(), name, kind, size: file.size, preview: url, attachment: { name, media_type: file.type, data } } };
    }
    if (file.size > LIMITS.textBytes) return { ok: false, error: `${name}：文件太大（${fmtSize(file.size)}，单个上限 ${LIMITS.textBytes >> 10} KB）` };
    const text = await file.text();
    if (text.includes("\u0000") || text.includes("�")) return { ok: false, error: `${name}：看起来不是文本文件（含二进制内容或不是 UTF-8 编码）` };
    return { ok: true, item: { id: newId(), name, kind, size: file.size, attachment: { name, text } } };
  } catch (e) {
    return { ok: false, error: `${name}：${e instanceof Error ? e.message : "读取失败"}` };
  }
}
