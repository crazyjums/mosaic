// 更新日志的展示辅助：分类的显示名和颜色、更新建议文案、条目统计。
// 分类和建议等级由后端（internal/update/changelog.go）算好，这里只负责显示。

import type { ChangeKind, ReleaseNotes, UpdateInfo } from "../bridge";

/** 分类的显示顺序、中文名（渲染时再翻译）和颜色（CSS 变量，跟随皮肤）。 */
export const KINDS: Record<ChangeKind, { label: string; color: string }> = {
  breaking: { label: "不兼容变更", color: "var(--danger)" },
  security: { label: "安全", color: "var(--danger)" },
  added: { label: "新增", color: "var(--ok)" },
  changed: { label: "变更", color: "var(--accent)" },
  fixed: { label: "修复", color: "var(--warn)" },
  deprecated: { label: "弃用", color: "var(--muted)" },
  removed: { label: "移除", color: "var(--muted)" },
};

/** 更新建议：文案（中文原文）、颜色、一句话说明。 */
export const LEVELS = {
  critical: { label: "安全更新，建议尽快更新", color: "var(--danger)" },
  recommended: { label: "建议更新", color: "var(--warn)" },
  optional: { label: "可选更新", color: "var(--muted)" },
} as const;

/** 统计各分类的条目数，按展示顺序返回；用于横幅上的"新增 3 · 修复 2"。 */
export function countByKind(releases: ReleaseNotes[]): { kind: ChangeKind; count: number }[] {
  const n: Partial<Record<ChangeKind, number>> = {};
  for (const r of releases) for (const s of r.sections) n[s.kind] = (n[s.kind] ?? 0) + s.items.length;
  return (Object.keys(KINDS) as ChangeKind[]).filter((k) => n[k]).map((kind) => ({ kind, count: n[kind]! }));
}

/** 这次更新是否有可显示的结构化日志。 */
export const hasReleases = (info: Pick<UpdateInfo, "releases">): boolean => !!info.releases?.some((r) => r.sections.length > 0 || r.summary);
