// 输入框里的 / 补全：输入以 / 开头、还没有空白的一段文字时，弹出菜单，里面有技能和插件提供的命令。
// 只匹配"/名字"这种形状：/etc/hosts 这样的路径（含第二个 /）和 /名字 后面已经有内容的消息不会触发。

import type { SkillInfo, SlashCommandInfo } from "../bridge";

/** 菜单里的一项：技能（模型按需读取）或插件命令（发送前展开成提示词）。 */
export interface SlashItem {
  kind: "skill" | "command";
  name: string;
  description: string;
  /** 右侧的小标签：项目 / 个人 / 插件名 */
  tag: string;
  /** 悬停提示（出处和路径） */
  title: string;
}

/** 返回 / 后面已经输入的部分（可能是空串）；不是在输入 /命令 时返回 null。 */
export function slashQuery(text: string): string | null {
  const m = /^\/([^\s/]*)$/.exec(text);
  return m ? m[1] : null;
}

/**
 * 把技能和插件命令合成菜单项。tag 用的是中文原文的 key（项目 / 个人），由界面翻译；
 * 插件的技能和命令的标签是插件名本身，不需要翻译。
 */
export function toSlashItems(skills: SkillInfo[], commands: SlashCommandInfo[]): SlashItem[] {
  const fromSkills = skills.map<SlashItem>((s) => ({
    kind: "skill",
    name: s.name,
    description: s.description,
    tag: s.source === "project" ? "项目" : s.source === "user" ? "个人" : s.origin,
    title: `${s.origin}\n${s.dir}`,
  }));
  const fromCommands = commands.map<SlashItem>((c) => ({ kind: "command", name: c.name, description: c.description, tag: c.plugin_name, title: c.plugin_id }));
  // 命令在前：它们是用户专门装的，更可能是想要的
  return [...fromCommands, ...fromSkills];
}

/** 按输入过滤：名字以它开头的排最前，其次名字包含，最后简介包含；不区分大小写。 */
export function filterSlash<T extends { name: string; description: string }>(list: T[], query: string): T[] {
  const q = query.toLowerCase();
  if (!q) return list;
  const rank = (s: T) => {
    const n = s.name.toLowerCase();
    if (n.startsWith(q)) return 0;
    if (n.includes(q)) return 1;
    return s.description.toLowerCase().includes(q) ? 2 : 3;
  };
  return list
    .map((s) => [s, rank(s)] as const)
    .filter(([, r]) => r < 3)
    .sort((a, b) => a[1] - b[1] || a[0].name.localeCompare(b[0].name))
    .map(([s]) => s);
}

/** 旧名字，保持兼容。 */
export const filterSkills = filterSlash;

/** 选中后填进输入框的文字：/名字 加一个空格，接着写要做什么。 */
export const skillText = (s: { name: string }): string => `/${s.name} `;

/** 把 "/名字 参数" 拆成名字和参数；不是这种形状（普通文字、路径）返回 null。 */
export function parseSlash(msg: string): { name: string; args: string } | null {
  const m = /^\/([^\s/]+)(?:\s+([\s\S]*))?$/.exec(msg);
  return m ? { name: m[1], args: m[2] ?? "" } : null;
}
