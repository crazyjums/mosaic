// MCP 设置表单与 mcp.ServerConfig 之间的转换。参数、环境变量、请求头在表单里是多行文本，便于粘贴。

import type { MCPServerConfig } from "../bridge";

export interface MCPForm {
  name: string;
  transport: "stdio" | "http";
  command: string;
  args: string; // 每行一个
  env: string; // 每行 KEY=VALUE
  url: string;
  headers: string; // 每行 Name: Value
  risk: "read" | "write" | "exec";
  disabled: boolean;
  cwd: string;
}

const lines = (s: string) =>
  s
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);

/** 解析 "KEY=VALUE" / "Name: Value" 形式的多行文本；sep 第一次出现的位置之前是键。 */
export function parsePairs(s: string, sep: "=" | ":"): Record<string, string> {
  const out: Record<string, string> = {};
  for (const l of lines(s)) {
    const i = l.indexOf(sep);
    if (i <= 0) continue;
    out[l.slice(0, i).trim()] = l.slice(i + 1).trim();
  }
  return out;
}

const formatPairs = (m: Record<string, string> | null | undefined, sep: string) =>
  Object.entries(m ?? {})
    .map(([k, v]) => `${k}${sep}${v}`)
    .join("\n");

export function toForm(c: MCPServerConfig): MCPForm {
  return {
    name: c.name,
    transport: c.transport,
    command: c.command ?? "",
    args: (c.args ?? []).join("\n"),
    env: formatPairs(c.env, "="),
    url: c.url ?? "",
    headers: formatPairs(c.headers, ": "),
    risk: c.risk === "read" || c.risk === "write" ? c.risk : "exec",
    disabled: !!c.disabled,
    cwd: c.cwd ?? "",
  };
}

/** 表单 → 配置。只保留当前传输方式需要的字段，切换传输方式时不会把另一种的残留一起保存。 */
export function fromForm(f: MCPForm): MCPServerConfig {
  const base = { name: f.name.trim(), transport: f.transport, risk: f.risk, disabled: f.disabled || undefined };
  if (f.transport === "http") {
    const headers = parsePairs(f.headers, ":");
    return { ...base, url: f.url.trim(), headers: Object.keys(headers).length ? headers : undefined };
  }
  const env = parsePairs(f.env, "=");
  return {
    ...base,
    command: f.command.trim(),
    args: lines(f.args).length ? lines(f.args) : undefined,
    env: Object.keys(env).length ? env : undefined,
    cwd: f.cwd.trim() || undefined,
  };
}

export const EMPTY_FORM: MCPForm = { name: "", transport: "stdio", command: "", args: "", env: "", url: "", headers: "", risk: "exec", disabled: false, cwd: "" };

/** 示例：官方的文件系统 server（需要 Node.js）。 */
export const EXAMPLE_FORM: MCPForm = {
  ...EMPTY_FORM,
  name: "filesystem",
  command: "npx",
  args: "-y\n@modelcontextprotocol/server-filesystem\n/path/to/allowed/dir",
};
