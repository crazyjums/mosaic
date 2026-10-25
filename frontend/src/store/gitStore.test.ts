import { beforeEach, describe, expect, it, vi } from "vitest";
import { demoCommitDetail, demoCommits, demoDiff, demoGitOverview } from "../bridge/demoGit";

// 用演示仓库（bridge/demoGit.ts）充当后端；测试环境没有 window，不能走 backend() 的自动选择。
vi.mock("../bridge", () => ({
  backend: () => ({
    gitOverview: async () => demoGitOverview(),
    gitLog: async (skip: number, limit: number) => demoCommits.slice(skip, skip + limit),
    gitCommit: async (hash: string) => demoCommitDetail(hash),
    gitCommitDiff: async (_h: string, path: string) => demoDiff(path),
    gitWorkingDiff: async (path: string) => demoDiff(path),
  }),
}));

import { useGit } from "./gitStore";

describe("gitStore", () => {
  beforeEach(() => useGit.setState({ open: false, selected: null, detail: null, commits: [], overview: null, diff: null, file: null, error: "" }));

  it("打开后加载概览与提交，并默认选中 HEAD 的第一个文件 diff", async () => {
    await useGit.getState().refresh();
    const s = useGit.getState();
    expect(s.error).toBe("");
    expect(s.overview?.status.branch).toBe("main");
    expect(s.commits.length).toBeGreaterThan(5);
    expect(s.selected).toBe(s.overview?.status.head);
    expect(s.detail?.hash).toBe(s.selected);
    expect(s.file).toBe(s.detail?.files[0].path);
    expect(s.diff?.text).toContain("@@");
  });

  it("点击分支定位到它指向的提交", async () => {
    await useGit.getState().refresh();
    const b = useGit.getState().overview!.branches.find((x) => x.name === "feature/attachments")!;
    await useGit.getState().reveal(b);
    expect(useGit.getState().selected).toBe(b.hash);
    expect(useGit.getState().tab).toBe("history");
  });

  it("快速切换提交时，较慢的旧请求不会覆盖新的选择", async () => {
    await useGit.getState().refresh();
    const [a, b] = useGit.getState().commits.slice(1, 3);
    const p1 = useGit.getState().select(a.hash);
    const p2 = useGit.getState().select(b.hash);
    await Promise.all([p1, p2]);
    expect(useGit.getState().selected).toBe(b.hash);
    expect(useGit.getState().detail?.hash).toBe(b.hash);
  });
});
