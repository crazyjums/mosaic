import type { ToolView } from "./reducer";

export function summarizeTurn(tools: ToolView[]) {
  const files = new Set<string>();
  const commands: { callId: string; command: string; status: ToolView["status"] }[] = [];
  for (const tool of tools) {
    const input = tool.input && typeof tool.input === "object" ? tool.input as Record<string, unknown> : {};
    if (tool.risk === "exec" && typeof input.command === "string") commands.push({ callId: tool.callId, command: input.command, status: tool.status });
    if (tool.status !== "done" || tool.risk !== "write") continue;
    for (const key of ["path", "file_path", "destination"]) if (typeof input[key] === "string" && input[key]) files.add(input[key]);
    if (Array.isArray(input.changes)) for (const change of input.changes) {
      if (change && typeof change === "object" && typeof change.path === "string" && change.path) files.add(change.path);
    }
  }
  return { files: [...files], commands, failed: tools.filter((tool) => tool.status === "error" || tool.status === "denied") };
}
