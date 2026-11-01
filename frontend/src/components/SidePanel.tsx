import { useEffect, useMemo, useState } from "react";
import { t } from "../i18n";
import { absPath, collectArtifacts } from "../store/artifacts";
import { useChat } from "../store/chatStore";
import { useUIPrefs, type UIPrefs } from "../store/uiPrefs";
import ArtifactsPanel, { ArtifactViewer } from "./ArtifactsPanel";
import ContextPanel from "./ContextPanel";

// 右侧面板：一排标签（上下文 / 产物）+ 当前标签的内容。
//
//   - 最右边有一条始终可见的窄标签栏（像编辑器的侧边栏，只放图标，悬停显示名称）：点一个标签展开它；再点当前标签，或点面板上的 ›，把面板折叠回标签栏；
//   - 两个标签共用固定宽度（26rem），切换标签时对话区及输入框不发生横向跳动；\n//   - 面板从顶到底占满高度，内容区自己滚动；
//   - 窗口不够宽时整个右侧面板隐藏（和以前的上下文明细一致），标题栏上的按钮也一起隐藏。

type Tab = UIPrefs["sideTab"];

const TABS: { id: Tab; label: string; icon: string }[] = [
  { id: "context", label: "上下文", icon: "▤" },
  { id: "artifacts", label: "产物", icon: "◧" },
];

/** 当前会话的产物数量（标签和标题栏按钮上的角标）。 */
export function useArtifactCount(): number {
  const timeline = useChat((s) => s.timeline);
  const tools = useChat((s) => s.tools);
  return useMemo(() => collectArtifacts(timeline, tools).length, [timeline, tools]);
}

/** 点一个标签：折叠着就展开并切到它；已经是当前标签且展开着就折叠。标题栏的按钮和侧边标签栏共用。 */
export function toggleSideTab(tab: Tab) {
  const { prefs, update } = useUIPrefs.getState();
  if (prefs.contextPanel && prefs.sideTab === tab) void update({ contextPanel: false });
  else void update({ contextPanel: true, sideTab: tab });
}

export default function SidePanel() {
  const open = useUIPrefs((s) => s.prefs.contextPanel);
  const tab = useUIPrefs((s) => s.prefs.sideTab);
  const update = useUIPrefs((s) => s.update);
  const count = useArtifactCount();
  const [zoom, setZoom] = useState(false);
  const timeline = useChat((s) => s.timeline);
  const tools = useChat((s) => s.tools);
  const workspace = useChat((s) => s.workspace);
  const first = useMemo(() => collectArtifacts(timeline, tools)[0], [timeline, tools]);

  useEffect(() => {
    if (!zoom) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setZoom(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [zoom]);

  return (
    <div className="hidden h-full shrink-0 lg:flex">
      {open && (
        <section className="flex h-full w-[26rem] min-h-0 shrink-0 flex-col border-l border-line bg-side" aria-label={t(TABS.find((x) => x.id === tab)!.label)}>
          <nav className="m-3 flex shrink-0 gap-1 rounded-xl border border-line bg-panel p-1" aria-label={t("右侧面板")}>
            {TABS.map((x) => <button key={x.id}
              className={`flex min-w-0 flex-1 items-center justify-center gap-1 rounded-lg px-2 py-2 text-xs font-semibold ${tab === x.id ? "bg-accent text-on-accent" : "text-muted hover:bg-panel-2"}`}
              onClick={() => void update({ contextPanel: true, sideTab: x.id })}
              aria-pressed={tab === x.id}
            >{t(x.label)} {x.id === "artifacts" && count > 0 && <span className="rounded-full bg-panel/30 px-1.5 text-[10px]">{count}</span>}</button>)}
            {tab === "artifacts" && first && <button className="rounded-lg px-2 text-muted hover:bg-panel-2 hover:text-fg" onClick={() => setZoom(true)} aria-label={t("放大预览")}>⤢</button>}
            <button className="rounded-lg px-2 text-muted hover:bg-panel-2 hover:text-fg" onClick={() => void update({ contextPanel: false })} aria-label={t("折叠面板")}>›</button>
          </nav>
          {tab === "context" ? <ContextPanel active /> : <ArtifactsPanel />}
        </section>
      )}
      {zoom && first && <ZoomedArtifacts onClose={() => setZoom(false)} workspace={workspace} />}
    </div>
  );
}

/** 放大预览：弹窗里重新选择产物，内容占满大部分屏幕。 */
function ZoomedArtifacts({ onClose, workspace }: { onClose: () => void; workspace: string }) {
  const timeline = useChat((s) => s.timeline);
  const tools = useChat((s) => s.tools);
  const list = useMemo(() => collectArtifacts(timeline, tools), [timeline, tools]);
  const [picked, setPicked] = useState(list[0]?.path);
  const current = list.find((a) => a.path === picked) ?? list[0];
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-4" onClick={onClose} role="dialog" aria-modal="true" aria-label={t("产物预览")}>
      <div className="flex h-[88vh] w-full max-w-6xl flex-col overflow-hidden rounded-modal border border-line bg-panel shadow-pop" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center gap-2 border-b border-line px-4 py-2.5">
          <select className="min-w-0 max-w-[60%] truncate rounded-ctl border border-line bg-panel px-2 py-1 text-sm" value={current?.path} onChange={(e) => setPicked(e.target.value)} aria-label={t("选择产物")} title={current ? absPath(workspace, current.path) : ""}>
            {list.map((a) => (
              <option key={a.path} value={a.path}>
                {a.path}
              </option>
            ))}
          </select>
          <div className="flex-1" />
          <button className="rounded px-1.5 text-muted hover:text-fg" onClick={onClose} aria-label={t("关闭")}>
            ✕
          </button>
        </div>
        <div className="flex min-h-0 flex-1 flex-col p-3">{current && <ArtifactViewer key={current.path} artifact={current} big />}</div>
      </div>
    </div>
  );
}
