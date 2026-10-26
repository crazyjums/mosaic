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
//   - 面板从顶到底占满高度，内容区自己滚动；
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
        <section className={`flex h-full min-h-0 flex-col border-l border-line bg-side ${tab === "artifacts" ? "w-[26rem]" : "w-72"}`} aria-label={t(TABS.find((x) => x.id === tab)!.label)}>
          <div className="flex h-10 shrink-0 items-center border-b border-line px-3">
            <span className="text-sm font-semibold">{tab === "context" ? t("上下文明细") : t("产物")}</span>
            {tab === "artifacts" && count > 0 && <span className="ml-1.5 rounded-full bg-panel-2 px-1.5 text-[10px] text-muted">{count}</span>}
            <div className="flex-1" />
            {tab === "artifacts" && first && (
              <button className="mr-1 rounded-ctl px-1.5 py-0.5 text-muted hover:bg-panel-2 hover:text-fg" onClick={() => setZoom(true)} title={t("放大预览")} aria-label={t("放大预览")}>
                ⤢
              </button>
            )}
            <button className="rounded-ctl px-1.5 py-0.5 text-muted hover:bg-panel-2 hover:text-fg" onClick={() => void update({ contextPanel: false })} title={t("折叠（点右侧的标签可以重新展开）")} aria-label={t("折叠面板")}>
              ›
            </button>
          </div>
          {tab === "context" ? <ContextPanel active /> : <ArtifactsPanel />}
        </section>
      )}
      {/* 侧边标签栏：折叠时也在，点标签展开 */}
      <nav className="flex h-full w-10 shrink-0 flex-col items-center gap-1 border-l border-line bg-side py-2" aria-label={t("右侧面板")}>
        {TABS.map((x) => {
          const active = open && tab === x.id;
          return (
            <button
              key={x.id}
              className={`flex w-8 shrink-0 flex-col items-center gap-1 rounded-ctl px-0.5 py-2 text-[11px] hover:bg-panel-2 focus-visible:outline-2 focus-visible:outline-accent ${active ? "bg-accent-soft text-fg" : "text-muted"}`}
              onClick={() => toggleSideTab(x.id)}
              aria-pressed={active}
              aria-label={t(x.label)}
              title={active ? `${t(x.label)} · ${t("折叠")}` : t(x.label)}
            >
              <span aria-hidden="true" className="shrink-0 text-base leading-none">{x.icon}</span>
              
              {x.id === "artifacts" && count > 0 && <span className="rounded-full bg-accent px-1 text-[9px] leading-3 text-on-accent">{count}</span>}
            </button>
          );
        })}
      </nav>
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
