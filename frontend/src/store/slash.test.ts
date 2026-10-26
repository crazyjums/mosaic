import { describe, expect, it } from "vitest";
import type { SkillInfo } from "../bridge";
import { filterSkills, filterSlash, parseSlash, skillText, slashQuery, toSlashItems } from "./slash";

const sk = (name: string, description = ""): SkillInfo => ({ name, description, source: "user", origin: ".claude/skills", dir: "/x/" + name });

describe("slashQuery", () => {
  it("只在输入 /技能名 时触发", () => {
    expect(slashQuery("/")).toBe("");
    expect(slashQuery("/pdf")).toBe("pdf");
    expect(slashQuery("/代码审查")).toBe("代码审查");
  });
  it("路径、已经有后续内容、普通文字都不触发", () => {
    expect(slashQuery("/etc/hosts")).toBeNull();
    expect(slashQuery("/pdf 做个报告")).toBeNull();
    expect(slashQuery("hello /pdf")).toBeNull();
    expect(slashQuery("")).toBeNull();
  });
});

describe("filterSkills", () => {
  const list = [sk("code-review", "审查代码"), sk("pdf-report", "把数据整理成 PDF 报告"), sk("deploy", "上线前检查 code 风格")];
  it("空查询返回全部", () => expect(filterSkills(list, "")).toHaveLength(3));
  it("名字前缀 > 名字包含 > 简介包含", () => {
    expect(filterSkills(list, "code").map((s) => s.name)).toEqual(["code-review", "deploy"]);
    expect(filterSkills(list, "report").map((s) => s.name)).toEqual(["pdf-report"]);
    expect(filterSkills(list, "PDF").map((s) => s.name)).toEqual(["pdf-report"]);
  });
  it("没有匹配返回空", () => expect(filterSkills(list, "zzz")).toEqual([]));
  it("选中后填入 /名字 加空格", () => expect(skillText(list[0])).toBe("/code-review "));
});

describe("插件命令混进菜单", () => {
  const skills = [sk("review", "审查"), { ...sk("feishu-docs", "读文档"), source: "plugin:feishu" as const, origin: "飞书" }];
  const cmds = [{ name: "feishu-summary", description: "总结文档", plugin_id: "feishu", plugin_name: "飞书" }];
  it("命令排在技能前面，标签区分来源", () => {
    const items = toSlashItems(skills, cmds);
    expect(items.map((i) => `${i.kind}:${i.name}:${i.tag}`)).toEqual(["command:feishu-summary:飞书", "skill:review:个人", "skill:feishu-docs:飞书"]);
    expect(filterSlash(items, "feishu").map((i) => i.name)).toEqual(["feishu-docs", "feishu-summary"]);
  });
  it("parseSlash 只认 /名字 参数", () => {
    expect(parseSlash("/hello 世界\n第二行")).toEqual({ name: "hello", args: "世界\n第二行" });
    expect(parseSlash("/hello")).toEqual({ name: "hello", args: "" });
    expect(parseSlash("/etc/hosts")).toBeNull();
    expect(parseSlash("hello /x")).toBeNull();
    expect(parseSlash("")).toBeNull();
  });
});
