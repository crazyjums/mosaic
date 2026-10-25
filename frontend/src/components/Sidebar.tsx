import { useEffect, useMemo, useRef, useState } from "react";
import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";
import { useUIPrefs } from "../store/uiPrefs";
import { filterSessions, GROUP_LABELS, groupSessions } from "../store/sessionGroups";
import { useAgentPhase } from "../store/useAgentPhase";
import EditableTitle from "./EditableTitle";
import MosaicMark from "./MosaicMark";
import { MOD_KEY } from "../store/keys";
import { baseName } from "./WorkspacePicker";
import { t } from "../i18n";

// 左侧会话栏：Logo、新建会话、搜索、按 今天 / 昨天 / 近 7 天 / 更早 分组的历史会话（点击恢复、双击或 ✎ 改名）。
//
// Logo 的金色拼块反映 Agent 当前的状态（空闲 / 工作中 / 需要你处理 / 刚完成），见 store/useAgentPhase.ts。
// 可以收起成一列图标（« 按钮或 Ctrl/⌘+B），收起状态保存在界面偏好里，下次启动保持；Ctrl/⌘+K 聚焦搜索。

/** 今天只显示时间，其他显示日期。 */
function fmtTime(iso: string): string {
  const d = new Date(iso);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  return sameDay ? d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : d.toLocaleDateString([], { month: "numeric", day: "numeric" });
}

export default function Sidebar() {
  const { sessions, sessionId, newSession, loadSession, deleteSession, renameSession, running, backendKind } = useChat();
  const openSettings = useSettings((s) => s.show);
  const collapsed = useUIPrefs((s) => s.prefs.sidebarCollapsed);
  const updatePrefs = useUIPrefs((s) => s.update);
  const [editing, setEditing] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);
  const phase = useAgentPhase();
  const groups = useMemo(() => groupSessions(filterSessions(sessions, query)), [sessions, query]);
  const toggle = () => void updatePrefs({ sidebarCollapsed: !useUIPrefs.getState().prefs.sidebarCollapsed });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "b") {
        e.preventDefault();
        toggle();
      }
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        if (useUIPrefs.getState().prefs.sidebarCollapsed) toggle();
        setTimeout(() => searchRef.current?.focus(), 0);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (collapsed) {
    return (
      <aside className="flex w-12 shrink-0 flex-col items-center gap-1 border-r border-line bg-side py-2">
        <MosaicMark size="sm" state={phase} className="mb-1 mt-1" />
        <button className="rail-btn" onClick={toggle} title={t("展开会话栏（Ctrl/⌘+B）")} aria-label={t("展开会话栏（Ctrl/⌘+B）")}>
          »
        </button>
        <button className="rail-btn" onClick={() => void newSession()} title={t("＋ 新会话")} aria-label={t("＋ 新会话")}>
          ＋
        </button>
        <div className="flex-1" />
        <button className="rail-btn" onClick={() => void openSettings()} title={t("⚙ 设置")} aria-label={t("⚙ 设置")}>
          ⚙
        </button>
      </aside>
    );
  }

  return (
    <aside className="flex w-[var(--sidebar-width)] shrink-0 flex-col border-r border-line bg-side">
      <div className="flex h-12 items-center gap-2.5 px-4">
        <MosaicMark size="sm" state={phase} decorative={false} />
        <span className="text-[17px] font-semibold tracking-tight">Mosaic</span>
        {backendKind === "demo" && <span className="rounded bg-warn/15 px-1.5 py-0.5 text-[10px] text-warn">{t("演示模式")}</span>}
        <div className="flex-1" />
        <button className="rounded-ctl px-1.5 text-muted hover:bg-panel-2 hover:text-fg" onClick={toggle} title={t("收起会话栏（Ctrl/⌘+B）")} aria-label={t("收起会话栏（Ctrl/⌘+B）")}>
          «
        </button>
      </div>
      <div className="space-y-2 px-3">
        <button
          className="w-full rounded-ctl bg-brand px-3 py-2 text-sm font-medium text-on-brand shadow-card hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50"
          onClick={() => void newSession()}
        >
          {t("＋ 新会话")}
        </button>
        <label className="flex items-center gap-2 rounded-ctl border border-line bg-panel px-2.5 py-1.5 text-sm focus-within:border-accent focus-within:ring-2 focus-within:ring-[var(--focus-ring)]">
          <span className="text-muted" aria-hidden="true">⌕</span>
          <input
            ref={searchRef}
            className="min-w-0 flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted"
            placeholder={t("搜索会话…")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && (setQuery(""), e.currentTarget.blur())}
            aria-label={t("搜索会话…")}
          />
          {query ? (
            <button className="text-xs text-muted hover:text-fg" onClick={() => setQuery("")} aria-label={t("清除搜索")}>
              ✕
            </button>
          ) : (
            <kbd className="rounded border border-line px-1 text-[10px] text-muted">{MOD_KEY} K</kbd>
          )}
        </label>
      </div>
      <nav className="mt-3 flex-1 overflow-y-auto px-2 pb-3">
        {sessions.length === 0 && <div className="px-2 py-2 text-xs text-muted">{t("还没有保存的会话")}</div>}
        {sessions.length > 0 && groups.length === 0 && <div className="px-2 py-2 text-xs text-muted">{t("没有匹配的会话")}</div>}
        {groups.map((g) => (
          <section key={g.key} className="mb-2">
            <div className="px-2 pb-1 pt-2 text-[11px] font-medium text-muted">{t(GROUP_LABELS[g.key])}</div>
            {g.items.map((s) => {
              const active = s.id === sessionId;
              return (
                <div
                  key={s.id}
                  className={`group relative flex cursor-pointer items-center rounded-ctl px-2.5 py-1.5 text-sm ${active ? "bg-accent-soft" : "hover:bg-panel-2/70"}`}
                  onClick={() => editing !== s.id && s.id !== sessionId && void loadSession(s.id)}
                  aria-current={active ? "true" : undefined}
                >
                  {/* 当前会话左侧的金色细条：与 Logo 里"正在执行"的金色拼块呼应 */}
                  {active && <span className="absolute inset-y-1.5 left-0 w-[3px] rounded-full bg-gold" aria-hidden="true" />}
                  <div className="min-w-0 flex-1">
                    <div className="flex">
                      <EditableTitle
                        value={s.title}
                        placeholder={t("（无标题）")}
                        editing={editing === s.id}
                        onEditingChange={(v) => setEditing(v ? s.id : null)}
                        onSave={(title) => renameSession(s.id, title)}
                        className={active ? "font-medium" : ""}
                      />
                    </div>
                    <div className="truncate text-[11px] text-muted" title={`${s.workspace ? s.workspace + "\n" : ""}${s.provider}/${s.model} · ${new Date(s.updated_at).toLocaleString()}`}>
                      {s.workspace && <>📁 {baseName(s.workspace)} · </>}
                      {s.provider}/{s.model} · {fmtTime(s.updated_at)}
                    </div>
                  </div>
                  {editing !== s.id && (
                    <div className="ml-1 hidden shrink-0 group-hover:flex">
                      <button
                        className="rounded px-1 text-xs text-muted hover:text-fg"
                        title={t("改名")}
                        aria-label={t("改名")}
                        onClick={(e) => {
                          e.stopPropagation();
                          setEditing(s.id);
                        }}
                      >
                        ✎
                      </button>
                      <button
                        className="rounded px-1 text-xs text-muted hover:text-danger disabled:opacity-40"
                        title={t("删除")}
                        disabled={running && s.id === sessionId}
                        onClick={(e) => {
                          e.stopPropagation();
                          if (confirm(t("删除会话「{title}」？", { title: s.title }))) void deleteSession(s.id);
                        }}
                      >
                        ✕
                      </button>
                    </div>
                  )}
                </div>
              );
            })}
          </section>
        ))}
      </nav>
      <div className="border-t border-line p-2">
        <button
          className="flex w-full items-center gap-2 rounded-ctl px-2 py-2 text-left text-sm text-muted hover:bg-panel-2 hover:text-fg disabled:opacity-50"
          onClick={() => void openSettings()}
        >
          {t("⚙ 设置")}
        </button>
      </div>
    </aside>
  );
}
