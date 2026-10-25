import { useEffect, useRef, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { backend } from "../bridge";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";

// 当前会话的工作区选择器：每个会话可以有自己的工作区（通常一个会话对应一个代码仓库）。
//
// 菜单里有：选择文件夹…、最近用过的工作区（从会话列表里取，不需要另外保存）、改回默认工作区。
// 运行中不能切换（一轮中途换目录，前后两次工具调用会落在不同的地方）。

/** 路径的最后一段（目录名），同时兼容 / 和 \。 */
export const baseName = (p: string) => p.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || p;

/** 最近用过的工作区：会话列表按更新时间排序，取不重复的工作区。 */
function useRecentWorkspaces(exclude: string): string[] {
  return useChat(
    useShallow((s) => {
      const out: string[] = [];
      for (const x of s.sessions) {
        const w = x.workspace;
        if (w && w !== exclude && !out.includes(w)) out.push(w);
        if (out.length >= 8) break;
      }
      return out;
    }),
  );
}

export default function WorkspacePicker({ variant, full = false }: { variant: "statusbar" | "card"; full?: boolean }) {
  const { workspace, isDefault, locked, missing, running, setSessionWorkspace } = useChat(
    useShallow((s) => ({
      workspace: s.workspace,
      isDefault: s.workspaceIsDefault,
      locked: s.workspaceLocked,
      missing: s.workspaceMissing,
      running: s.running,
      setSessionWorkspace: s.setSessionWorkspace,
    })),
  );
  const recent = useRecentWorkspaces(workspace);
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && setOpen(false);
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (!workspace) return null;
  const disabled = running || locked;
  const choose = async () => {
    setOpen(false);
    const dir = await backend().chooseDirectory(workspace);
    if (dir) await setSessionWorkspace(dir);
  };
  const pick = (dir: string) => {
    setOpen(false);
    void setSessionWorkspace(dir);
  };

  const title = locked
    ? t("工作区：{path}（由启动参数指定）", { path: workspace })
    : missing
      ? t("工作区 {path} 不存在，请重新选择", { path: workspace })
      : running
        ? t("运行中不能切换工作区")
        : t("当前会话的工作区：{path}{note}。点击切换", { path: workspace, note: isDefault ? t("（默认）") : "" });

  const menu = open && (
    <div
      className={`absolute z-30 w-80 overflow-hidden rounded-lg border border-line bg-panel text-left text-sm shadow-xl ${variant === "statusbar" ? "bottom-full left-0 mb-2" : "left-1/2 top-full mt-2 -translate-x-1/2"}`}
      role="menu"
    >
      <div className="border-b border-line px-3 py-2 text-[11px] text-muted">{t("当前会话的工作区")}</div>
      <button className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-panel-2" onClick={() => void choose()} role="menuitem">
        📂 {t("选择文件夹…")}
      </button>
      {recent.length > 0 && (
        <>
          <div className="border-t border-line px-3 pb-1 pt-2 text-[11px] text-muted">{t("最近用过")}</div>
          {recent.map((w) => (
            <button key={w} className="block w-full px-3 py-1.5 text-left hover:bg-panel-2" onClick={() => pick(w)} title={w} role="menuitem">
              <div className="truncate">{baseName(w)}</div>
              <div className="truncate font-mono text-[10px] text-muted">{w}</div>
            </button>
          ))}
        </>
      )}
      {!isDefault && (
        <button className="block w-full border-t border-line px-3 py-2 text-left text-muted hover:bg-panel-2 hover:text-fg" onClick={() => pick("")} role="menuitem">
          {t("改回默认工作区")}
        </button>
      )}
    </div>
  );

  if (variant === "card") {
    return (
      <div ref={ref} className="relative mx-auto mt-6 max-w-md">
        <button
          className={`flex w-full items-center gap-3 rounded-lg border bg-panel px-4 py-3 text-left hover:border-accent/60 disabled:cursor-not-allowed disabled:opacity-60 ${missing ? "border-danger/60" : "border-line"}`}
          onClick={() => setOpen(!open)}
          disabled={disabled}
          title={title}
        >
          <span className="text-xl">📁</span>
          <span className="min-w-0 flex-1">
            <span className="block text-[11px] text-muted">{isDefault ? t("工作区（默认）") : t("工作区")}</span>
            <span className="block truncate text-sm font-medium text-fg">{baseName(workspace)}</span>
            <span className="block truncate font-mono text-[10px] text-muted">{workspace}</span>
          </span>
          {!locked && <span className="shrink-0 text-xs text-accent">{t("更换")}</span>}
        </button>
        {missing && <div className="mt-1 text-xs text-danger">{t("这个目录不存在了，请重新选择")}</div>}
        {menu}
      </div>
    );
  }

  return (
    <div ref={ref} className="relative">
      <button
        className={`max-w-[20rem] truncate whitespace-nowrap hover:text-fg disabled:cursor-default disabled:hover:text-inherit ${missing ? "text-danger" : ""}`}
        onClick={() => setOpen(!open)}
        disabled={disabled}
        title={title}
      >
        {missing ? "⚠" : "📁"} {full ? workspace : baseName(workspace)}
        {!disabled && <span className="ml-0.5 text-[10px] opacity-60">▾</span>}
      </button>
      {menu}
    </div>
  );
}
