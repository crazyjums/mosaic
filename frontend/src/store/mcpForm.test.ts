import { describe, expect, it } from "vitest";
import { fromForm, parsePairs, toForm, EMPTY_FORM } from "./mcpForm";

describe("mcpForm", () => {
  it("parses pairs, keeping separators in values", () => {
    expect(parsePairs("A=1\n  B = x=y \nbad\n=no", "=")).toEqual({ A: "1", B: "x=y" });
    expect(parsePairs("Authorization: Bearer a:b", ":")).toEqual({ Authorization: "Bearer a:b" });
  });

  it("round-trips a stdio config", () => {
    const cfg = { name: "fs", transport: "stdio" as const, command: "npx", args: ["-y", "pkg"], env: { TOKEN: "t" }, risk: "read" as const };
    expect(fromForm(toForm(cfg))).toEqual(cfg);
  });

  it("keeps only the fields of the selected transport", () => {
    const f = { ...EMPTY_FORM, name: " gh ", transport: "http" as const, url: "https://x/mcp", command: "leftover", headers: "X-Key: 1" };
    expect(fromForm(f)).toEqual({ name: "gh", transport: "http", url: "https://x/mcp", headers: { "X-Key": "1" }, risk: "exec" });
  });
});
