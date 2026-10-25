import type { GitBranch, GitCommit, GitCommitDetail, GitDiff, GitOverview } from "./types";

// 演示模式（浏览器预览 / 截图）用的假仓库：有主干、功能分支、热修复分支、合并和标签，
// 足够把 Git 窗口的分支图各种形状都展示出来。真实数据来自后端的 internal/gitview。

const DAY = 86400;
const NOW = Math.floor(Date.now() / 1000);

// 从新到旧；[id, 父提交, 主题, 作者, 距今天数, 引用]
type Row = [string, string[], string, string, number, string[]?];
const ROWS: Row[] = [
  ["a1f4c9e", ["8d2b7a1", "e51c0d3"], "Merge branch 'feature/git-window' into main", "crazyjums", 0.2, ["*main", "origin/main"]],
  ["e51c0d3", ["b7a90f2"], "feat(git): 分支图的车道布局与 SVG 渲染", "crazyjums", 0.4, ["feature/git-window"]],
  ["8d2b7a1", ["4c3e8b6", "d90a1f5"], "Merge branch 'hotfix/queue-dup' into main", "crazyjums", 0.9],
  ["b7a90f2", ["6f1d2c8"], "feat(git): 提交详情与文件 diff 面板", "crazyjums", 1.1],
  ["d90a1f5", ["4c3e8b6"], "fix(queue): 排空队列时同步标记 sending，避免重复发送", "crazyjums", 1.3, ["hotfix/queue-dup"]],
  ["4c3e8b6", ["6f1d2c8"], "docs: 更新 roadmap，标注队列与附件已完成", "crazyjums", 1.6],
  ["6f1d2c8", ["2a7e5b9"], "feat(git): 只读 gitview 服务与 Wails 绑定", "crazyjums", 2.1],
  ["2a7e5b9", ["f03c6d4", "9b4e2a7"], "Merge branch 'feature/attachments' into main", "crazyjums", 2.8, ["v0.4.0"]],
  ["9b4e2a7", ["c8d15f0"], "feat(composer): 支持图片与 Markdown/纯文本附件", "crazyjums", 3.0, ["feature/attachments"]],
  ["f03c6d4", ["5e8a1b2"], "feat(core): 上下文压缩 SummaryCompactor", "crazyjums", 3.4],
  ["c8d15f0", ["5e8a1b2"], "feat(core): 附件校验与围栏内联", "crazyjums", 3.6],
  ["5e8a1b2", ["71f9c30"], "refactor: 产品更名为 Mosaic", "crazyjums", 5.2],
  ["71f9c30", ["0b6d4e1"], "feat(ui): 汝窑天青皮肤与品牌校准", "crazyjums", 7.5, ["v0.3.0"]],
  ["0b6d4e1", ["3e2a9c5"], "feat: 技能（Skills）读取能力", "crazyjums", 9.1],
  ["3e2a9c5", [], "chore: 初始提交", "crazyjums", 14],
];

export const demoCommits: GitCommit[] = ROWS.map(([id, parents, subject, author, days, refs]) => ({
  hash: id.padEnd(40, "0"),
  short: id,
  parents: parents.map((p) => p.padEnd(40, "0")),
  author,
  time: Math.round(NOW - days * DAY),
  subject,
  refs: refs?.map((r) => {
    const head = r.startsWith("*");
    const name = r.replace(/^\*/, "");
    const kind = /^v\d/.test(name) ? "tag" : name.startsWith("origin/") ? "remote" : "branch";
    return { name, kind, ...(head ? { head: true } : {}) } as NonNullable<GitCommit["refs"]>[number];
  }),
}));

export function demoGitOverview(): GitOverview {
  const branches: GitBranch[] = [];
  for (const c of demoCommits) {
    for (const r of c.refs ?? []) {
      branches.push({ name: r.name, kind: r.kind, hash: c.hash, current: r.head, time: c.time, subject: c.subject, ...(r.kind === "branch" && r.name === "main" ? { upstream: "origin/main", ahead: 0, behind: 0 } : {}) });
    }
  }
  return {
    status: {
      branch: "main",
      detached: false,
      head: demoCommits[0].hash,
      upstream: "origin/main",
      files: [
        { path: "frontend/src/components/StatusBar.tsx", index: " ", work: "M" },
        { path: "docs/usage.md", index: "M", work: " " },
        { path: "internal/gitview/gitview.go", index: "A", work: "M" },
        { path: "notes/todo.md", index: "?", work: "?" },
      ],
    },
    branches,
  };
}

export function demoCommitDetail(hash: string): GitCommitDetail {
  const c = demoCommits.find((x) => x.hash.startsWith(hash)) ?? demoCommits[0];
  const files = [
    { path: "internal/gitview/gitview.go", status: "M", added: 42, deleted: 7 },
    { path: "frontend/src/components/GitWindow.tsx", status: "A", added: 188, deleted: 0 },
    { path: "docs/usage.md", status: "M", added: 12, deleted: 2 },
  ];
  return {
    ...c,
    email: "dev@example.com",
    body: c.parents.length > 1 ? "" : "这是演示模式里的示例提交，真实内容来自你的仓库。",
    files: c.parents.length > 1 ? files.slice(0, 1) : files,
    added: files.reduce((n, f) => n + f.added, 0),
    deleted: files.reduce((n, f) => n + f.deleted, 0),
  };
}

export function demoDiff(path: string): GitDiff {
  return {
    path,
    text: [
      `diff --git a/${path} b/${path}`,
      "index 3b18e51..c2a4f7d 100644",
      `--- a/${path}`,
      `+++ b/${path}`,
      "@@ -10,7 +10,9 @@ func (r Repo) Log(ctx context.Context, skip, limit int) ([]Commit, error) {",
      "     if limit <= 0 || limit > MaxLogPage {",
      "         limit = MaxLogPage",
      "     }",
      "-    out, err := r.run(ctx, \"log\", \"--all\")",
      "+    out, err := r.run(ctx, maxListBytes, \"log\", \"--all\", \"--date-order\")",
      "+    // 只读：不修改仓库",
      "     if err != nil {",
      "         return nil, err",
      "     }",
    ].join("\n"),
  };
}
