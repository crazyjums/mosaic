import { describe, expect, it } from "vitest";
import { absPath, collectArtifacts, kindOf, looksLikeFilePath } from "./artifacts";
import type { AssistantItem, ToolView } from "./reducer";

const tool = (callId: string, name: string, input: unknown, status: ToolView["status"] = "done"): ToolView => ({ callId, name, input, risk: "write", status });
const msg = (...callIds: string[]): AssistantItem => ({
  kind: "assistant", id: "m" + callIds.join(), agentId: "main", step: 1, provider: "p", model: "m", streaming: false,
  blocks: callIds.map((c) => ({ type: "tool" as const, callId: c })),
});

describe("collectArtifacts", () => {
  it("收集写出的文件：同一文件只保留一次，最新写的在最前面", () => {
    const tools = {
      a: tool("a", "write_file", { path: "out/flow.svg", content: "<svg/>" }),
      b: tool("b", "write_file", { path: "report.html", content: "x" }),
      c: tool("c", "edit_file", { path: "out/flow.svg", old_string: "a", new_string: "b" }),
      d: tool("d", "move_file", { source: "a.md", destination: "docs/a.md" }),
    };
    const got = collectArtifacts([msg("a", "b"), msg("c", "d")], tools);
    expect(got.map((x) => x.path)).toEqual(["docs/a.md", "out/flow.svg", "report.html"]);
    expect(got.map((x) => x.kind)).toEqual(["markdown", "svg", "html"]);
    expect(got[1]).toMatchObject({ tool: "edit_file", callId: "c" });
  });

  it("失败、被拒绝、还没执行完的调用，以及只读工具都不算产物", () => {
    const tools = {
      a: tool("a", "write_file", { path: "x.html" }, "denied"),
      b: tool("b", "write_file", { path: "y.html" }, "running"),
      c: tool("c", "read_file", { path: "z.html" }),
      d: tool("d", "write_file", { path: "" }),
      e: tool("e", "shell", { command: "echo > w.html" }),
    };
    expect(collectArtifacts([msg("a", "b", "c", "d", "e")], tools)).toEqual([]);
  });

  it("忽略非 assistant 条目和缺失的工具", () => {
    expect(collectArtifacts([{ kind: "user", id: "u", text: "hi" }, msg("zz")], {})).toEqual([]);
  });
});

describe("kindOf", () => {
  it("按扩展名分类（忽略大小写和查询串）", () => {
    expect(["a.HTML", "b.svg", "c.mmd", "d.gv", "e.md", "f.PNG", "g.json", "h.exe", "noext"].map(kindOf)).toEqual(["html", "svg", "mermaid", "dot", "markdown", "image", "text", "other", "other"]);
  });
});

describe("absPath", () => {
  it("拼成绝对路径，分隔符跟工作区一致", () => {
    expect(absPath("/home/u/proj", "out/r.html")).toBe("/home/u/proj/out/r.html");
    expect(absPath("/home/u/proj/", "./out/r.html")).toBe("/home/u/proj/out/r.html");
    expect(absPath("C:\\work\\proj", "out/r.html")).toBe("C:\\work\\proj\\out\\r.html");
    expect(absPath("/w", "a/../b.html")).toBe("/w/b.html");
  });
  it("已经是绝对路径就原样返回", () => {
    expect(absPath("/w", "/tmp/x.html")).toBe("/tmp/x.html");
    expect(absPath("C:\\w", "D:\\x\\y.html")).toBe("D:\\x\\y.html");
    expect(absPath("", "a.html")).toBe("a.html");
  });
});

describe("looksLikeFilePath", () => {
  it("行内代码像文件路径才变成链接", () => {
    expect(["report.html", "out/flow.svg", "C:\\x\\a.md", "./a.json"].every(looksLikeFilePath)).toBe(true);
    expect(["foo()", "a b.html", "https://x.com/a.html", "run.sh", "a.exe", "*.html", "x".repeat(300) + ".html"].some(looksLikeFilePath)).toBe(false);
  });
});
