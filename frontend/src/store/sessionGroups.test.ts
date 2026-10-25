import { describe, expect, it } from "vitest";
import { filterSessions, groupOf, groupSessions } from "./sessionGroups";

const now = new Date(2026, 9, 25, 15, 0, 0); // 2026-10-25 15:00（本地时区）
const at = (d: number, h = 9) => new Date(2026, 9, 25 - d, h, 0, 0).toISOString();

describe("会话分组", () => {
  it("按自然日划分，不按 24 小时", () => {
    expect(groupOf(at(0, 0), now)).toBe("today");
    expect(groupOf(at(1, 23), now)).toBe("yesterday");
    expect(groupOf(at(3), now)).toBe("week");
    expect(groupOf(at(6), now)).toBe("week");
    expect(groupOf(at(7), now)).toBe("older");
    expect(groupOf(at(-1), now)).toBe("today"); // 未来时间
  });
  it("只返回非空的组，组内保持原顺序", () => {
    const items = [{ updated_at: at(0), id: "a" }, { updated_at: at(0, 8), id: "b" }, { updated_at: at(30), id: "c" }];
    const g = groupSessions(items, now);
    expect(g.map((x) => x.key)).toEqual(["today", "older"]);
    expect(g[0].items.map((x) => x.id)).toEqual(["a", "b"]);
  });
});

describe("会话搜索", () => {
  const items = [
    { title: "修复登录", provider: "openai", model: "gpt-5", workspace: "/home/me/webapp" },
    { title: "写文档", provider: "anthropic", model: "claude-opus-5-5" },
  ];
  it("匹配标题、模型和工作区，不区分大小写", () => {
    expect(filterSessions(items, "登录")).toHaveLength(1);
    expect(filterSessions(items, "CLAUDE")).toHaveLength(1);
    expect(filterSessions(items, "webapp")).toHaveLength(1);
    expect(filterSessions(items, "  ")).toHaveLength(2);
    expect(filterSessions(items, "不存在")).toHaveLength(0);
  });
});
