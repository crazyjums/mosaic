import type { AssistantItem } from "./reducer";

// 过程折叠时，在“正在思考”卡片上露出的一句真实思考摘要。
//
// 只用模型真正输出过的思考文字：取最近一段非空文字，压成单行并截断。
// 没有思考文字（模型没返回、或被厂商加密/脱敏）时返回 null，调用方就不显示这一行——绝不编造内容。

const MAX_CHARS = 160;

export function thinkingSummary(items: AssistantItem[]): string | null {
  for (let i = items.length - 1; i >= 0; i--) {
    const blocks = items[i].blocks;
    for (let j = blocks.length - 1; j >= 0; j--) {
      const b = blocks[j];
      if (b.type !== "thinking" || b.redacted) continue;
      const paras = b.text.split(/\n\s*\n/).map((p) => p.replace(/\s+/g, " ").trim()).filter(Boolean);
      const last = paras[paras.length - 1];
      if (!last) continue;
      const chars = Array.from(last);
      return chars.length > MAX_CHARS ? chars.slice(0, MAX_CHARS).join("") + "…" : last;
    }
  }
  return null;
}
