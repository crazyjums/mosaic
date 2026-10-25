import type { ReleaseNotes, UpdateInfo } from "../bridge";
import { t } from "../i18n";
import { countByKind, hasReleases, KINDS, LEVELS } from "../store/changelog";

// 更新日志：按版本、按类型分组显示。跳过了几个版本时，把当前版本之后的全部版本依次列出，
// 最新的默认展开，更早的折叠。旧格式的清单（没有结构化日志）退回显示纯文本说明。

/** 更新建议标签：安全更新 / 建议更新 / 可选更新。 */
export function LevelBadge({ info }: { info: UpdateInfo }) {
  if (!info.level) return null;
  const lv = LEVELS[info.level];
  return (
    <span className="rounded-full border px-2 py-0.5 text-[11px] font-medium" style={{ color: lv.color, borderColor: "currentColor" }}>
      {t(lv.label)}
    </span>
  );
}

/** 一行摘要："新增 3 · 修复 2 · 安全 1"。 */
export function ChangeSummary({ releases }: { releases: ReleaseNotes[] }) {
  const counts = countByKind(releases);
  if (counts.length === 0) return null;
  return (
    <span className="text-xs text-muted">
      {counts.map((c, i) => (
        <span key={c.kind}>
          {i > 0 && " · "}
          <span style={{ color: KINDS[c.kind].color }}>{t(KINDS[c.kind].label)}</span> {c.count}
        </span>
      ))}
    </span>
  );
}

function Release({ r, open }: { r: ReleaseNotes; open: boolean }) {
  return (
    <details open={open} className="rounded-md border border-line bg-panel">
      <summary className="cursor-pointer select-none px-3 py-1.5 text-xs">
        <span className="font-semibold">v{r.version}</span>
        {r.date && <span className="ml-2 text-muted">{r.date}</span>}
        {r.summary && <span className="ml-2 text-muted">{r.summary}</span>}
      </summary>
      <div className="space-y-2 border-t border-line px-3 py-2">
        {r.sections.map((s) => (
          <div key={s.kind}>
            <div className="mb-0.5 text-[11px] font-medium" style={{ color: KINDS[s.kind].color }}>
              {t(KINDS[s.kind].label)}
            </div>
            <ul className="list-disc space-y-0.5 pl-5 text-xs leading-relaxed">
              {s.items.map((it, i) => (
                <li key={i}>{it}</li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </details>
  );
}

export default function ChangeLog({ info }: { info: UpdateInfo }) {
  if (!hasReleases(info)) {
    return info.notes ? <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded-md border border-line bg-panel p-3 text-xs text-muted">{info.notes}</pre> : null;
  }
  const rs = info.releases ?? [];
  return (
    <div className="space-y-2">
      {info.breaking && <div className="rounded-md border border-warn/50 bg-warn/10 px-3 py-1.5 text-xs text-warn">{t("这次更新包含不兼容变更，更新前请先阅读下面的说明。")}</div>}
      {rs.length > 1 && <div className="text-[11px] text-muted">{t("你跳过了 {n} 个版本，下面是它们的全部变更。", { n: rs.length - 1 })}</div>}
      {rs.map((r, i) => (
        <Release key={r.version} r={r} open={i === 0} />
      ))}
    </div>
  );
}
