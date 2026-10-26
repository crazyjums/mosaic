import { language, t } from "../i18n";
import type { PluginCatalogEntry } from "../bridge";

// 插件市场的卡片、精选位和分类。纯展示：安装、更新由上层（PluginSettings）处理。

/** 分类：key 是 registry.json 里的值，文字在界面里翻译。 */
export const CATEGORIES: [string, string][] = [
  ["dev", "开发"],
  ["writing", "写作"],
  ["office", "办公"],
  ["data", "数据"],
  ["demo", "示例"],
];

const PALETTE = ["#e0614d", "#2f8f6b", "#d98a1f", "#7a56d6", "#2b7bb9", "#c2518f", "#4f7bff", "#6b7d96"];

/** 图标颜色：同一个插件永远是同一个颜色（按 id 哈希），不需要插件提供图片。 */
export function iconColor(id: string): string {
  let h = 0;
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

/** 图标上的字：名字以英文单词开头（Git、SQL）就用这个词，否则取第一个字。 */
export function iconText(name: string): string {
  const m = /^[A-Za-z]{2,4}(?![A-Za-z])/.exec(name.trim());
  return m ? m[0] : Array.from(name.trim())[0] ?? "?";
}

export function PluginIcon({ id, name, size = 40 }: { id: string; name: string; size?: number }) {
  return (
    <span
      className="flex shrink-0 items-center justify-center rounded-[11px] font-bold text-white"
      style={{ width: size, height: size, background: iconColor(id), fontSize: size * 0.36 }}
      aria-hidden="true"
    >
      {iconText(name)}
    </span>
  );
}

const summaryOf = (c: PluginCatalogEntry) => c.summary?.[language()] ?? c.summary?.zh ?? c.summary?.en ?? "";

function Tag({ children, warn }: { children: string; warn?: boolean }) {
  return <span className={`rounded-full px-2 py-px text-[11px] ${warn ? "bg-warn/15 text-warn" : "bg-panel-2 text-muted"}`}>{children}</span>;
}

/** 卡片上的小标签：它带来什么、需要什么。 */
function tags(c: PluginCatalogEntry): { text: string; warn?: boolean }[] {
  const out: { text: string; warn?: boolean }[] = [];
  if (c.sensitive) out.push({ text: t("读取私密内容 · 每次询问"), warn: true });
  if (c.skills) out.push({ text: t("{n} 个技能", { n: c.skills }) });
  if (c.commands) out.push({ text: t("{n} 个命令", { n: c.commands }) });
  if (c.servers) out.push({ text: t("{n} 个工具服务", { n: c.servers }) });
  for (const r of c.requires ?? []) out.push({ text: t("需要 {cmd}", { cmd: r }) });
  return out;
}

export function Action({ c, busy, onGet }: { c: PluginCatalogEntry; busy: boolean; onGet: () => void }) {
  if (c.installed && !c.update_available) return <span className="shrink-0 rounded-full bg-panel-2 px-3 py-1 text-xs text-muted">✓ {t("已安装")}</span>;
  return (
    <button className="shrink-0 rounded-full bg-accent-soft px-4 py-1 text-xs font-semibold text-accent hover:brightness-95 disabled:opacity-50" disabled={busy} onClick={onGet}>
      {busy ? t("下载中…") : c.installed ? t("更新到 v{v}", { v: c.version ?? "" }) : t("安装")}
    </button>
  );
}

export function FeaturedCard({ c, busy, onGet }: { c: PluginCatalogEntry; busy: boolean; onGet: () => void }) {
  return (
    <div className="mb-4 flex items-center gap-4 rounded-modal border border-line bg-accent-soft/60 p-4">
      <PluginIcon id={c.id} name={c.name} size={56} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-base font-semibold">{c.name}</span>
          <span className="rounded-full bg-accent-soft px-2 py-px text-[11px] text-accent">{t("精选")}</span>
        </div>
        <p className="mt-0.5 text-sm text-muted">{summaryOf(c)}</p>
        <div className="mt-2 flex flex-wrap gap-1.5">
          {tags(c).map((x) => (
            <Tag key={x.text} warn={x.warn}>
              {x.text}
            </Tag>
          ))}
        </div>
      </div>
      <Action c={c} busy={busy} onGet={onGet} />
    </div>
  );
}

export function PluginCard({ c, busy, onGet }: { c: PluginCatalogEntry; busy: boolean; onGet: () => void }) {
  return (
    <div className="flex min-h-[148px] flex-col gap-2 rounded-modal border border-line bg-panel p-3.5">
      <div className="flex items-center gap-3">
        <PluginIcon id={c.id} name={c.name} />
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold">{c.name}</div>
          <div className="truncate text-[11px] text-muted">
            {c.version ? `v${c.version} · ` : ""}
            {c.kind === "bundled" ? t("内置") : t("来自 Git 仓库")}
          </div>
        </div>
      </div>
      <p className="line-clamp-2 flex-1 text-[12.5px] leading-relaxed text-muted">{summaryOf(c)}</p>
      <div className="flex items-end gap-2">
        <div className="flex min-w-0 flex-1 flex-wrap gap-1.5">
          {tags(c)
            .slice(0, 3)
            .map((x) => (
              <Tag key={x.text} warn={x.warn}>
                {x.text}
              </Tag>
            ))}
        </div>
        <Action c={c} busy={busy} onGet={onGet} />
      </div>
    </div>
  );
}

export const categoryLabel = (key: string) => t(CATEGORIES.find(([k]) => k === key)?.[1] ?? key);

/** 搜索：名字、简介（两种语言）、ID 里包含关键字。 */
export function matches(c: PluginCatalogEntry, q: string): boolean {
  const s = q.trim().toLowerCase();
  if (!s) return true;
  return [c.id, c.name, ...Object.values(c.summary ?? {})].some((x) => x.toLowerCase().includes(s));
}
