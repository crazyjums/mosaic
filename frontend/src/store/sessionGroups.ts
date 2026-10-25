// 会话栏的分组：今天 / 昨天 / 近 7 天 / 更早，按更新时间（本地时区的自然日）划分。
// 输入顺序保持不变（后端已经按更新时间倒序），所以每组内部仍是最新的在前。

export type SessionGroupKey = "today" | "yesterday" | "week" | "older";

export const GROUP_LABELS: Record<SessionGroupKey, string> = {
  today: "今天",
  yesterday: "昨天",
  week: "近 7 天",
  older: "更早",
};

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

export function groupOf(updatedAt: string | number | Date, now: Date = new Date()): SessionGroupKey {
  const days = Math.round((startOfDay(now) - startOfDay(new Date(updatedAt))) / 86_400_000);
  if (days <= 0) return "today"; // 未来的时间（时钟不准）也算今天
  if (days === 1) return "yesterday";
  if (days < 7) return "week";
  return "older";
}

export function groupSessions<T extends { updated_at: string }>(items: T[], now: Date = new Date()): { key: SessionGroupKey; items: T[] }[] {
  const by = new Map<SessionGroupKey, T[]>();
  for (const it of items) {
    const k = groupOf(it.updated_at, now);
    by.set(k, [...(by.get(k) ?? []), it]);
  }
  return (["today", "yesterday", "week", "older"] as const).filter((k) => by.has(k)).map((key) => ({ key, items: by.get(key)! }));
}

/** 搜索：标题、厂商 / 模型、工作区目录名里包含关键字（不区分大小写）。空关键字返回全部。 */
export function filterSessions<T extends { title: string; provider: string; model: string; workspace?: string }>(items: T[], query: string): T[] {
  const q = query.trim().toLowerCase();
  if (!q) return items;
  return items.filter((s) => [s.title, `${s.provider}/${s.model}`, s.workspace ?? ""].some((x) => x.toLowerCase().includes(q)));
}
