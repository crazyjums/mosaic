// 输入框里的 / 补全：输入以 / 开头、还没有空白的一段文字时，弹出技能菜单。
// 只匹配"/技能名"这种形状：/etc/hosts 这样的路径（含第二个 /）和 /技能名 后面已经有内容的消息不会触发。

import type { SkillInfo } from "../bridge";

/** 返回 / 后面已经输入的部分（可能是空串）；不是在输入 /命令 时返回 null。 */
export function slashQuery(text: string): string | null {
  const m = /^\/([^\s/]*)$/.exec(text);
  return m ? m[1] : null;
}

/** 按输入过滤技能：名字以它开头的排最前，其次名字包含，最后简介包含；不区分大小写。 */
export function filterSkills(list: SkillInfo[], query: string): SkillInfo[] {
  const q = query.toLowerCase();
  if (!q) return list;
  const rank = (s: SkillInfo) => {
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

/** 选中技能后填进输入框的文字：/技能名 加一个空格，接着写要做什么。 */
export const skillText = (s: SkillInfo): string => `/${s.name} `;
