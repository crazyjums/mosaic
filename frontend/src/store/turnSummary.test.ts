import { describe, expect, it } from "vitest";
import type { ToolView } from "./reducer";
import { summarizeTurn } from "./turnSummary";

const tool = (callId: string, name: string, risk: ToolView["risk"], status: ToolView["status"], input: unknown): ToolView => ({ callId, name, risk, status, input });

describe("turn summary", () => {
  it("lists only successful reported file changes and retains denied commands", () => {
    const result = summarizeTurn([
      tool("1", "write_file", "write", "done", { path: "src/a.ts" }),
      tool("2", "apply_patch", "write", "done", { changes: [{ path: "src/a.ts" }, { path: "src/b.ts" }] }),
      tool("3", "edit_file", "write", "denied", { path: "secret" }),
      tool("4", "shell", "exec", "denied", { command: "rm x" }),
    ]);
    expect(result.files).toEqual(["src/a.ts", "src/b.ts"]);
    expect(result.commands).toEqual([{ callId: "4", command: "rm x", status: "denied" }]);
    expect(result.failed).toHaveLength(2);
  });
});
