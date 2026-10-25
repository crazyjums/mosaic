// 解析状态栏脚本输出里的 ANSI 颜色码（SGR：ESC[...m），转换成带样式的片段。
//
// 现成的状态栏脚本（例如为 Claude Code 写的）普遍用 ANSI 颜色，原样显示会出现一堆 "[32m"。
// 只支持 SGR，其他控制序列（光标移动等）直接丢弃——单行状态栏用不到它们。

export interface AnsiSegment {
  text: string;
  fg?: string;
  bg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

// 16 色：与常见终端的默认配色接近，深浅主题下都能看清。
const BASIC = ["#4b5563", "#ef4444", "#22c55e", "#eab308", "#3b82f6", "#a855f7", "#06b6d4", "#d1d5db"];
const BRIGHT = ["#6b7280", "#f87171", "#4ade80", "#facc15", "#60a5fa", "#c084fc", "#22d3ee", "#f9fafb"];

function color256(n: number): string | undefined {
  if (n < 8) return BASIC[n];
  if (n < 16) return BRIGHT[n - 8];
  if (n < 232) {
    const i = n - 16;
    const v = (x: number) => (x === 0 ? 0 : 55 + x * 40);
    return `rgb(${v(Math.floor(i / 36))},${v(Math.floor(i / 6) % 6)},${v(i % 6)})`;
  }
  if (n < 256) {
    const g = 8 + (n - 232) * 10;
    return `rgb(${g},${g},${g})`;
  }
  return undefined;
}

const ESCAPE = /\x1b\[([0-9;?]*)([A-Za-z])/g;

export function parseAnsi(input: string): AnsiSegment[] {
  const out: AnsiSegment[] = [];
  let style: Omit<AnsiSegment, "text"> = {};
  let last = 0;
  const push = (text: string) => {
    // 去掉其余不可见的控制字符（例如单独的 ESC、BEL）
    text = text.replace(/[\x00-\x08\x0b-\x1f\x7f]/g, "");
    if (text) out.push({ text, ...style });
  };
  for (const m of input.matchAll(ESCAPE)) {
    push(input.slice(last, m.index));
    last = m.index! + m[0].length;
    if (m[2] !== "m") continue;
    const codes = m[1] === "" ? [0] : m[1].split(";").map((x) => parseInt(x, 10) || 0);
    for (let i = 0; i < codes.length; i++) {
      const c = codes[i];
      if (c === 0) style = {};
      else if (c === 1) style.bold = true;
      else if (c === 2) style.dim = true;
      else if (c === 3) style.italic = true;
      else if (c === 4) style.underline = true;
      else if (c === 22) style.bold = style.dim = undefined;
      else if (c === 23) style.italic = undefined;
      else if (c === 24) style.underline = undefined;
      else if (c >= 30 && c <= 37) style.fg = BASIC[c - 30];
      else if (c >= 90 && c <= 97) style.fg = BRIGHT[c - 90];
      else if (c === 39) style.fg = undefined;
      else if (c >= 40 && c <= 47) style.bg = BASIC[c - 40];
      else if (c >= 100 && c <= 107) style.bg = BRIGHT[c - 100];
      else if (c === 49) style.bg = undefined;
      else if (c === 38 || c === 48) {
        // 38;5;n（256 色）或 38;2;r;g;b（真彩色）
        let col: string | undefined;
        if (codes[i + 1] === 5) {
          col = color256(codes[i + 2]);
          i += 2;
        } else if (codes[i + 1] === 2) {
          col = `rgb(${codes[i + 2] ?? 0},${codes[i + 3] ?? 0},${codes[i + 4] ?? 0})`;
          i += 4;
        }
        if (c === 38) style.fg = col;
        else style.bg = col;
      }
    }
    style = { ...style };
  }
  push(input.slice(last));
  return out;
}
