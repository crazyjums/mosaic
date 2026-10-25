import { useEffect, useState } from "react";
import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";
import { useUIPrefs } from "../store/uiPrefs";
import EditableTitle from "./EditableTitle";
import { baseName } from "./WorkspacePicker";
import { t } from "../i18n";

// 左侧会话栏：新建会话、历史会话列表（点击恢复、双击或 ✎ 改名）。
//
// 可以收起成一列图标（« 按钮或 Ctrl/⌘+B），收起状态保存在界面偏好里，下次启动保持。
export default function Sidebar() {
  const { sessions, sessionId, newSession, loadSession, deleteSession, renameSession, running, backendKind } = useChat();
  const openSettings = useSettings((s) => s.show);
  const collapsed = useUIPrefs((s) => s.prefs.sidebarCollapsed);
  const updatePrefs = useUIPrefs((s) => s.update);
  const [editing, setEditing] = useState<string | null>(null);
  const toggle = () => void updatePrefs({ sidebarCollapsed: !useUIPrefs.getState().prefs.sidebarCollapsed });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "b") {
        e.preventDefault();
        toggle();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (collapsed) {
    return (
      <aside className="flex w-12 shrink-0 flex-col items-center gap-1 border-r border-line bg-panel py-2">
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
    <aside className="flex w-64 shrink-0 flex-col border-r border-line bg-panel">
      <div className="flex h-12 items-center gap-2 px-4">
        <span className="font-semibold">myllmapp</span>
        {backendKind === "demo" && <span className="rounded bg-warn/15 px-1.5 py-0.5 text-[10px] text-warn">{t("演示模式")}</span>}
        <div className="flex-1" />
        <button className="rounded px-1.5 text-muted hover:bg-panel-2 hover:text-fg" onClick={toggle} title={t("收起会话栏（Ctrl/⌘+B）")} aria-label={t("收起会话栏（Ctrl/⌘+B）")}>
          «
        </button>
      </div>
      <div className="px-3">
        <button
          className="w-full rounded-lg border border-line px-3 py-2 text-left text-sm hover:bg-panel-2 disabled:opacity-50"
          onClick={() => void newSession()}
        >
          {t("＋ 新会话")}
        </button>
      </div>
      <div className="mt-4 px-4 text-[11px] uppercase tracking-wide text-muted">{t("历史会话")}</div>
      <nav className="mt-1 flex-1 overflow-y-auto px-2 pb-3">
        {sessions.length === 0 && <div className="px-2 py-2 text-xs text-muted">{t("还没有保存的会话")}</div>}
        {sessions.map((s) => (
          <div
            key={s.id}
            className={`group flex cursor-pointer items-center rounded-md px-2 py-1.5 text-sm ${s.id === sessionId ? "bg-panel-2" : "hover:bg-panel-2/60"}`}
            onClick={() => editing !== s.id && s.id !== sessionId && void loadSession(s.id)}
          >
            <div className="min-w-0 flex-1">
              <div className="flex">
                <EditableTitle
                  value={s.title}
                  placeholder={t("（无标题）")}
                  editing={editing === s.id}
                  onEditingChange={(v) => setEditing(v ? s.id : null)}
                  onSave={(title) => renameSession(s.id, title)}
                />
              </div>
              <div className="truncate text-[11px] text-muted" title={s.workspace || undefined}>
                {s.workspace && <>📁 {baseName(s.workspace)} · </>}
                {s.provider}/{s.model} · {new Date(s.updated_at).toLocaleString()}
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
        ))}
      </nav>
      <div className="border-t border-line p-2">
        <button
          className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm text-muted hover:bg-panel-2 hover:text-fg disabled:opacity-50"
          onClick={() => void openSettings()}
        >
          {t("⚙ 设置")}
        </button>
      </div>
    </aside>
  );
}
