import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";

// 左侧会话栏：新建会话、历史会话列表（点击恢复）。
export default function Sidebar() {
  const { sessions, sessionId, newSession, loadSession, deleteSession, running, backendKind } = useChat();
  const openSettings = useSettings((s) => s.show);

  return (
    <aside className="flex w-64 shrink-0 flex-col border-r border-line bg-panel">
      <div className="flex h-12 items-center justify-between px-4">
        <span className="font-semibold">myllmapp</span>
        {backendKind === "demo" && <span className="rounded bg-warn/15 px-1.5 py-0.5 text-[10px] text-warn">演示模式</span>}
      </div>
      <div className="px-3">
        <button
          className="w-full rounded-lg border border-line px-3 py-2 text-left text-sm hover:bg-panel-2 disabled:opacity-50"
          onClick={() => void newSession()}
          disabled={running}
        >
          ＋ 新会话
        </button>
      </div>
      <div className="mt-4 px-4 text-[11px] uppercase tracking-wide text-muted">历史会话</div>
      <nav className="mt-1 flex-1 overflow-y-auto px-2 pb-3">
        {sessions.length === 0 && <div className="px-2 py-2 text-xs text-muted">还没有保存的会话</div>}
        {sessions.map((s) => (
          <div
            key={s.id}
            className={`group flex cursor-pointer items-center rounded-md px-2 py-1.5 text-sm ${s.id === sessionId ? "bg-panel-2" : "hover:bg-panel-2/60"}`}
            onClick={() => s.id !== sessionId && !running && void loadSession(s.id)}
          >
            <div className="min-w-0 flex-1">
              <div className="truncate">{s.title || "（无标题）"}</div>
              <div className="truncate text-[11px] text-muted">
                {s.provider}/{s.model} · {new Date(s.updated_at).toLocaleString()}
              </div>
            </div>
            <button
              className="ml-1 hidden rounded px-1 text-xs text-muted hover:text-danger group-hover:block disabled:opacity-40"
              title="删除"
              disabled={running && s.id === sessionId}
              onClick={(e) => {
                e.stopPropagation();
                if (confirm(`删除会话「${s.title}」？`)) void deleteSession(s.id);
              }}
            >
              ✕
            </button>
          </div>
        ))}
      </nav>
      <div className="border-t border-line p-2">
        <button
          className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm text-muted hover:bg-panel-2 hover:text-fg disabled:opacity-50"
          onClick={() => void openSettings()}
          disabled={running}
          title={running ? "运行中不能修改设置" : undefined}
        >
          ⚙ 设置
        </button>
      </div>
    </aside>
  );
}
