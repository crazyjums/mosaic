// 演示模式（浏览器预览 / 截图）里"工作区文件"的示意内容：按扩展名返回一份样例，真实内容来自后端读取的工作区文件。

const SVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 420 220" font-family="sans-serif" font-size="13">
<rect width="420" height="220" fill="#f8fafc"/>
<rect x="20" y="85" width="90" height="46" rx="8" fill="#dbeafe" stroke="#2563eb"/><text x="65" y="113" text-anchor="middle">读取 diff</text>
<rect x="165" y="85" width="90" height="46" rx="8" fill="#dbeafe" stroke="#2563eb"/><text x="210" y="113" text-anchor="middle">解析 AST</text>
<rect x="310" y="85" width="90" height="46" rx="8" fill="#dcfce7" stroke="#16a34a"/><text x="355" y="113" text-anchor="middle">输出影响面</text>
<path d="M110 108H165M255 108H310" stroke="#475569" stroke-width="2" marker-end="url(#a)"/>
<defs><marker id="a" markerWidth="8" markerHeight="8" refX="6" refY="4" orient="auto"><path d="M0 0L8 4L0 8z" fill="#475569"/></marker></defs></svg>`;

const HTML = `<!doctype html><html><head><meta charset="utf-8"><style>body{font-family:system-ui;margin:24px;color:#0f172a}h1{font-size:20px}.card{border:1px solid #e2e8f0;border-radius:10px;padding:12px 16px;margin:12px 0}.ok{color:#16a34a}table{border-collapse:collapse;width:100%}td,th{border:1px solid #e2e8f0;padding:6px 10px;text-align:left}</style></head><body><h1>影响面分析报告（示例）</h1><div class="card"><b>结论</b>：本次改动影响 <span class="ok">3 个调用方</span>，建议补充回归测试。</div><table><tr><th>模块</th><th>风险</th></tr><tr><td>identify</td><td>中</td></tr><tr><td>callgraph</td><td>低</td></tr></table></body></html>`;

const MERMAID = `flowchart TD
  A[读取 diff] --> B{有 base AST?}
  B -- 是 --> C[直接对比]
  B -- 否 --> D[补建 base AST]
  D --> C
  C --> E[调用图遍历]
  E --> F[输出影响面]`;

const DOT = `digraph G {
  rankdir=LR; node [shape=box, style="rounded,filled", fillcolor="#dbeafe", fontname="sans-serif"];
  diff -> parse -> callgraph -> report;
  parse -> cache [style=dashed];
  cache -> callgraph [style=dashed];
}`;

const MD = `# 示例笔记

这是演示模式里的 Markdown 文件。

- 支持列表
- 支持 **加粗** 和 \`代码\`

\`\`\`mermaid
graph LR; A-->B; B-->C;
\`\`\`
`;

export function demoFileText(path: string): string {
  const ext = path.toLowerCase().split(".").pop() ?? "";
  switch (ext) {
    case "svg":
      return SVG;
    case "html":
    case "htm":
      return HTML;
    case "mmd":
    case "mermaid":
      return MERMAID;
    case "dot":
    case "gv":
      return DOT;
    case "md":
    case "markdown":
      return MD;
    default:
      return `（演示模式）${path} 的内容`;
  }
}
