import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { GitBranch, GitChangedFile, GitCommit, GitFileChange, GitRef } from "../bridge";
import { t } from "../i18n";
import { layoutGraph } from "../store/gitGraph";
import { useGit } from "../store/gitStore";
import GitGraphRow, { graphWidth, ROW_H } from "./GitGraph";
import { copyText } from "./Markdown";

// Git 窗口（只读）：左侧分支/标签，中间是带分支图的提交历史（或工作区变更），右侧是提交详情与 diff。
// 数据来自后端 internal/gitview；这里不提供任何会修改仓库的操作。

export default function GitWindow() {
  const open = useGit((s) => s.open);
  const hide = useGit((s) => s.hide);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") hide();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, hide]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-4" onClick={hide} role="dialog" aria-modal="true" aria-label={t("Git")}>
      <div className="flex h-[88vh] w-full max-w-[1500px] flex-col overflow-hidden rounded-modal border border-line bg-panel shadow-pop" onClick={(e) => e.stopPropagation()}>
        <Header />
        <Body />
      </div>
    </div>
  );
}

function Header() {
  const { overview, tab, setTab, refresh, loading, hide } = useGit();
  const st = overview?.status;
  const changes = st?.files.length ?? 0;
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line px-4 py-3">
      <div className="flex min-w-0 items-center gap-2">
        <span className="text-accent" aria-hidden="true">⎇</span>
        <span className="truncate text-[15px] font-semibold">{st ? (st.detached ? t("分离的 HEAD") : st.branch || "—") : "Git"}</span>
        {st?.upstream && (
          <span className="truncate text-xs text-muted">
            → {st.upstream}
            {(st.ahead ?? 0) > 0 && <span className="ml-1.5 text-ok">↑{st.ahead}</span>}
            {(st.behind ?? 0) > 0 && <span className="ml-1.5 text-warn">↓{st.behind}</span>}
          </span>
        )}
      </div>
      <div className="flex items-center gap-1 rounded-ctl bg-side p-0.5 text-sm" role="tablist">
        <TabButton active={tab === "history"} onClick={() => setTab("history")}>
          {t("历史")}
        </TabButton>
        <TabButton active={tab === "changes"} onClick={() => setTab("changes")}>
          {t("变更")}
          {changes > 0 && <span className="ml-1.5 rounded-full bg-warn/15 px-1.5 text-[11px] text-warn">{changes}</span>}
        </TabButton>
      </div>
      <span className="text-xs text-muted">{t("只读：窗口里的操作不会修改你的仓库")}</span>
      <div className="flex-1" />
      <button className="btn" onClick={() => void refresh()} disabled={loading}>
        {loading ? t("刷新中…") : t("刷新")}
      </button>
      <button className="rounded px-1.5 text-muted hover:text-fg" onClick={hide} aria-label={t("关闭")}>
        ✕
      </button>
    </div>
  );
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      role="tab"
      aria-selected={active}
      className={`rounded-ctl px-3 py-1 ${active ? "bg-panel font-medium text-fg shadow-card" : "text-muted hover:text-fg"}`}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

function Body() {
  const { error, loading, overview, tab } = useGit();
  if (error) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center text-sm">
        <div className="text-danger">{error}</div>
        <div className="text-xs text-muted">{t("Git 窗口查看的是当前会话工作区所在的 git 仓库。")}</div>
      </div>
    );
  }
  if (!overview) return <div className="flex flex-1 items-center justify-center text-sm text-muted">{loading ? t("正在读取仓库…") : ""}</div>;
  return (
    <div className="flex min-h-0 flex-1">
      <RefsPanel />
      {tab === "history" ? <HistoryView /> : <ChangesView />}
    </div>
  );
}

// ---- 左：分支与标签 ----

function RefsPanel() {
  const overview = useGit((s) => s.overview)!;
  const reveal = useGit((s) => s.reveal);
  const groups: [string, GitBranch[]][] = [
    [t("本地分支"), overview.branches.filter((b) => b.kind === "branch")],
    [t("远端分支"), overview.branches.filter((b) => b.kind === "remote")],
    [t("标签"), overview.branches.filter((b) => b.kind === "tag")],
  ];
  return (
    <nav className="hidden w-56 shrink-0 overflow-y-auto border-r border-line bg-side p-2 text-sm md:block" aria-label={t("分支与标签")}>
      {groups.map(([title, list]) => (
        <details key={title} open={title !== t("标签") || list.length <= 8} className="mb-2">
          <summary className="cursor-pointer select-none px-1.5 py-1 text-xs font-medium text-muted">
            {title} <span className="opacity-60">{list.length}</span>
          </summary>
          {list.length === 0 && <div className="px-2 py-1 text-xs text-muted">{t("（无）")}</div>}
          {list.map((b) => (
            <button
              key={b.kind + b.name}
              className="flex w-full items-center gap-1.5 rounded-ctl px-2 py-1 text-left hover:bg-accent-soft"
              onClick={() => void reveal(b)}
              title={b.subject ? `${b.name}\n${b.subject}` : b.name}
            >
              <span className={b.current ? "text-accent" : "text-muted"} aria-hidden="true">{b.current ? "●" : b.kind === "tag" ? "◆" : "○"}</span>
              <span className={`min-w-0 flex-1 truncate ${b.current ? "font-semibold" : ""}`}>{b.name}</span>
              {(b.ahead ?? 0) > 0 && <span className="text-[11px] text-ok">↑{b.ahead}</span>}
              {(b.behind ?? 0) > 0 && <span className="text-[11px] text-warn">↓{b.behind}</span>}
            </button>
          ))}
        </details>
      ))}
    </nav>
  );
}

// ---- 中：提交历史 + 分支图 ----

function HistoryView() {
  const { commits, hasMore, loadingMore, loadMore, selected, select } = useGit();
  const [q, setQ] = useState("");
  const layout = useMemo(() => layoutGraph(commits), [commits]);
  const width = graphWidth(layout.lanes);
  const needle = q.trim().toLowerCase();
  const matches = (c: GitCommit) => !needle || c.subject.toLowerCase().includes(needle) || c.author.toLowerCase().includes(needle) || c.hash.startsWith(needle);
  const listRef = useRef<HTMLDivElement>(null);

  // 选中的提交（例如点分支定位过来）滚动到可见位置
  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  }, [selected, commits.length]);

  return (
    <div className="flex min-w-0 flex-1">
      <section className="flex min-w-0 flex-1 flex-col">
        <div className="flex items-center gap-2 border-b border-line px-3 py-2">
          <input
            className="input h-8 max-w-sm flex-1 text-sm"
            placeholder={t("搜索已加载的提交")}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            aria-label={t("搜索提交")}
          />
          <span className="text-xs text-muted">{t("已加载 {n} 个提交", { n: commits.length })}</span>
        </div>
        <div ref={listRef} className="min-h-0 flex-1 overflow-auto" role="listbox" aria-label={t("提交历史")}>
          {commits.length === 0 && <div className="p-8 text-center text-sm text-muted">{t("这个仓库还没有提交")}</div>}
          {commits.map((c, i) => (
            <CommitRow key={c.hash} c={c} row={layout.rows[i]} lanes={layout.lanes} width={width} selected={c.hash === selected} dim={!matches(c)} onSelect={() => void select(c.hash)} />
          ))}
          {hasMore && (
            <div className="p-3 text-center">
              <button className="btn" onClick={() => void loadMore()} disabled={loadingMore}>
                {loadingMore ? t("加载中…") : t("加载更多")}
              </button>
            </div>
          )}
        </div>
      </section>
      <DetailPanel />
    </div>
  );
}

function CommitRow({ c, row, lanes, width, selected, dim, onSelect }: { c: GitCommit; row: ReturnType<typeof layoutGraph>["rows"][number]; lanes: number; width: number; selected: boolean; dim: boolean; onSelect: () => void }) {
  return (
    <div
      role="option"
      aria-selected={selected}
      tabIndex={0}
      className={`flex cursor-pointer items-center gap-2 border-b border-line/40 pr-3 text-sm hover:bg-accent-soft/60 focus-visible:outline-2 focus-visible:outline-accent ${selected ? "bg-accent-soft" : ""} ${dim ? "opacity-35" : ""}`}
      style={{ height: ROW_H, contentVisibility: "auto", containIntrinsicSize: `0 ${ROW_H}px` }}
      onClick={onSelect}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect();
        }
      }}
    >
      <div style={{ width }} className="shrink-0">
        <GitGraphRow row={row} lanes={lanes} merge={c.parents.length > 1} selected={selected} />
      </div>
      <div className="flex min-w-0 flex-1 items-center gap-1.5">
        {c.refs?.map((r) => <RefBadge key={r.kind + r.name} r={r} />)}
        <span className="min-w-0 truncate">{c.subject}</span>
      </div>
      <span className="hidden w-28 shrink-0 truncate text-xs text-muted lg:block">{c.author}</span>
      <span className="w-24 shrink-0 text-right text-xs tabular-nums text-muted">{fmtAgo(c.time)}</span>
      <span className="hidden w-16 shrink-0 font-mono text-xs text-muted xl:block">{c.short}</span>
    </div>
  );
}

function RefBadge({ r }: { r: GitRef }) {
  const cls =
    r.kind === "tag"
      ? "bg-warn/15 text-warn"
      : r.kind === "remote"
        ? "bg-side text-muted ring-1 ring-line"
        : r.head
          ? "bg-accent text-on-accent"
          : "bg-accent-soft text-accent";
  return (
    <span className={`shrink-0 rounded px-1.5 py-px text-[11px] font-medium leading-4 ${cls}`} title={r.kind === "tag" ? t("标签") : r.kind === "remote" ? t("远端分支") : t("本地分支")}>
      {r.kind === "tag" ? "◆ " : r.head ? "⎇ " : ""}
      {r.name}
    </span>
  );
}

// ---- 右：提交详情与 diff ----

function DetailPanel() {
  const { detail, detailError, selected, file, selectFile, diff, diffLoading, diffError } = useGit();
  const [copied, setCopied] = useState(false);
  return (
    <aside className="flex w-[34rem] max-w-[45%] shrink-0 flex-col border-l border-line" aria-label={t("提交详情")}>
      {!selected && <div className="p-6 text-sm text-muted">{t("选择一个提交查看详情")}</div>}
      {detailError && <div className="p-4 text-sm text-danger">{detailError}</div>}
      {detail && (
        <>
          <div className="max-h-[45%] shrink-0 overflow-y-auto border-b border-line p-4 text-sm">
            <h3 className="break-words text-[15px] font-semibold leading-snug">{detail.subject}</h3>
            {detail.body && <p className="mt-2 whitespace-pre-wrap break-words text-muted">{detail.body}</p>}
            <dl className="mt-3 grid grid-cols-[4.5rem_1fr] gap-x-2 gap-y-1 text-xs">
              <dt className="text-muted">{t("作者")}</dt>
              <dd className="min-w-0 truncate">{detail.author} &lt;{detail.email}&gt;</dd>
              <dt className="text-muted">{t("时间")}</dt>
              <dd>{new Date(detail.time * 1000).toLocaleString()}</dd>
              <dt className="text-muted">{t("提交号")}</dt>
              <dd className="flex items-center gap-2">
                <span className="min-w-0 truncate font-mono">{detail.hash}</span>
                <button
                  className="shrink-0 text-accent hover:underline"
                  onClick={async () => {
                    if (await copyText(detail.hash)) {
                      setCopied(true);
                      setTimeout(() => setCopied(false), 1500);
                    }
                  }}
                >
                  {copied ? t("已复制") : t("复制")}
                </button>
              </dd>
              {detail.parents.length > 0 && (
                <>
                  <dt className="text-muted">{t("父提交")}</dt>
                  <dd className="flex flex-wrap gap-2 font-mono">
                    {detail.parents.map((p) => (
                      <button key={p} className="text-accent hover:underline" onClick={() => void useGit.getState().select(p)}>
                        {p.slice(0, 7)}
                      </button>
                    ))}
                  </dd>
                </>
              )}
            </dl>
            <div className="mt-3 text-xs text-muted">
              {t("{n} 个文件变化", { n: detail.files.length })}
              <span className="ml-2 text-ok">+{detail.added}</span>
              <span className="ml-1.5 text-danger">−{detail.deleted}</span>
              {detail.parents.length > 1 && <span className="ml-2">{t("（合并提交：相对第一个父提交）")}</span>}
            </div>
            <ul className="mt-2 space-y-0.5">
              {detail.files.map((f) => (
                <FileRow key={f.path} f={f} active={f.path === file} onClick={() => void selectFile(f.path)} />
              ))}
            </ul>
          </div>
          <DiffView diff={diff} loading={diffLoading} error={diffError} />
        </>
      )}
    </aside>
  );
}

const STATUS_COLOR: Record<string, string> = { A: "text-ok", M: "text-warn", D: "text-danger", R: "text-accent", C: "text-accent", T: "text-muted", "?": "text-muted", U: "text-danger" };

function FileRow({ f, active, onClick }: { f: GitChangedFile; active: boolean; onClick: () => void }) {
  return (
    <li>
      <button className={`flex w-full items-center gap-2 rounded-ctl px-1.5 py-1 text-left text-xs hover:bg-accent-soft ${active ? "bg-accent-soft" : ""}`} onClick={onClick} title={f.orig_path ? `${f.orig_path} → ${f.path}` : f.path}>
        <span className={`w-4 shrink-0 text-center font-mono font-semibold ${STATUS_COLOR[f.status] ?? ""}`}>{f.status}</span>
        <span className="min-w-0 flex-1 truncate font-mono">{f.orig_path ? `${f.orig_path} → ${f.path}` : f.path}</span>
        {f.added >= 0 ? (
          <span className="shrink-0 tabular-nums">
            <span className="text-ok">+{f.added}</span> <span className="text-danger">−{f.deleted}</span>
          </span>
        ) : (
          <span className="shrink-0 text-muted">{t("二进制")}</span>
        )}
      </button>
    </li>
  );
}

function DiffView({ diff, loading, error }: { diff: ReturnType<typeof useGit.getState>["diff"]; loading: boolean; error: string }) {
  if (loading) return <div className="p-4 text-sm text-muted">{t("正在读取 diff…")}</div>;
  if (error) return <div className="p-4 text-sm text-danger">{error}</div>;
  if (!diff) return <div className="p-4 text-sm text-muted">{t("选择一个文件查看改动")}</div>;
  if (diff.note) return <div className="p-4 text-sm text-muted">{t(diff.note)}</div>;
  if (diff.binary) return <div className="p-4 text-sm text-muted">{t("二进制文件，没有文本 diff")}</div>;
  if (!diff.text) return <div className="p-4 text-sm text-muted">{t("没有文本改动（可能只改了权限或空白）")}</div>;
  const lines = diff.text.split("\n");
  return (
    <div className="min-h-0 flex-1 overflow-auto bg-code font-mono text-xs leading-5" aria-label={t("文件 diff")}>
      <div className="min-w-max py-1">
        {lines.map((line, i) => (
          <div key={i} className={`whitespace-pre px-3 ${lineClass(line)}`}>
            {line || " "}
          </div>
        ))}
        {diff.truncated && <div className="px-3 py-2 text-warn">{t("diff 太长，已截断")}</div>}
      </div>
    </div>
  );
}

function lineClass(l: string): string {
  if (l.startsWith("@@")) return "bg-accent-soft text-accent";
  if (l.startsWith("+++") || l.startsWith("---") || l.startsWith("diff ") || l.startsWith("index ")) return "text-muted";
  if (l.startsWith("+")) return "bg-ok/10 text-ok";
  if (l.startsWith("-")) return "bg-danger/10 text-danger";
  return "";
}

// ---- 变更页：工作区状态 ----

function ChangesView() {
  const overview = useGit((s) => s.overview)!;
  const { changeFile, selectChange, diff, diffLoading, diffError } = useGit();
  const files = overview.status.files;
  const staged = files.filter((f) => f.index !== " " && f.index !== "?");
  const unstaged = files.filter((f) => f.work !== " " && f.index !== "?");
  const untracked = files.filter((f) => f.index === "?");
  return (
    <div className="flex min-w-0 flex-1">
      <section className="w-96 shrink-0 overflow-y-auto border-r border-line p-3 text-sm">
        {files.length === 0 && <div className="p-6 text-center text-muted">{t("工作区很干净，没有未提交的改动")}</div>}
        <ChangeGroup title={t("已暂存")} list={staged} staged cur={changeFile} onPick={selectChange} />
        <ChangeGroup title={t("未暂存")} list={unstaged} staged={false} cur={changeFile} onPick={selectChange} />
        <ChangeGroup title={t("未跟踪")} list={untracked} staged={false} cur={changeFile} onPick={selectChange} />
      </section>
      <div className="flex min-w-0 flex-1 flex-col">
        {!changeFile ? <div className="p-6 text-sm text-muted">{t("选择一个文件查看改动")}</div> : <DiffView diff={diff} loading={diffLoading} error={diffError} />}
      </div>
    </div>
  );
}

function ChangeGroup({ title, list, staged, cur, onPick }: { title: string; list: GitFileChange[]; staged: boolean; cur: { path: string; staged: boolean } | null; onPick: (p: string, staged: boolean) => void }) {
  if (list.length === 0) return null;
  return (
    <div className="mb-4">
      <div className="mb-1 px-1 text-xs font-medium text-muted">
        {title} <span className="opacity-60">{list.length}</span>
      </div>
      <ul className="space-y-0.5">
        {list.map((f) => {
          const letter = staged ? f.index : f.index === "?" ? "?" : f.work;
          const active = cur?.path === f.path && cur.staged === staged;
          return (
            <li key={f.path + staged}>
              <button className={`flex w-full items-center gap-2 rounded-ctl px-1.5 py-1 text-left text-xs hover:bg-accent-soft ${active ? "bg-accent-soft" : ""}`} onClick={() => onPick(f.path, staged)} title={f.path}>
                <span className={`w-4 shrink-0 text-center font-mono font-semibold ${STATUS_COLOR[letter] ?? ""}`}>{letter}</span>
                <span className="min-w-0 flex-1 truncate font-mono">{f.orig_path ? `${f.orig_path} → ${f.path}` : f.path}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// ---- 时间 ----

export function fmtAgo(unix: number, now = Date.now()): string {
  const s = Math.max(0, Math.floor(now / 1000 - unix));
  if (s < 60) return t("刚刚");
  if (s < 3600) return t("{n} 分钟前", { n: Math.floor(s / 60) });
  if (s < 86400) return t("{n} 小时前", { n: Math.floor(s / 3600) });
  if (s < 86400 * 30) return t("{n} 天前", { n: Math.floor(s / 86400) });
  const d = new Date(unix * 1000);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

