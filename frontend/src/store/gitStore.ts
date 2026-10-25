// Git 窗口的状态：概览（状态 + 分支）、已加载的提交、当前选中的提交/文件及其 diff。
//
// 全部只读。每个异步请求都带一个序号，较慢的旧请求返回时丢弃，避免快速点击时详情“串位”。

import { create } from "zustand";
import { backend, type GitBranch, type GitCommit, type GitCommitDetail, type GitDiff, type GitOverview } from "../bridge";

export const PAGE = 200;

export type GitTab = "history" | "changes";

interface GitState {
  open: boolean;
  tab: GitTab;
  loading: boolean;
  error: string;
  overview: GitOverview | null;
  commits: GitCommit[];
  hasMore: boolean;
  loadingMore: boolean;

  selected: string | null;
  detail: GitCommitDetail | null;
  detailError: string;
  file: string | null;
  diff: GitDiff | null;
  diffLoading: boolean;
  diffError: string;
  /** 变更页里选中的文件（path + 是否看暂存区） */
  changeFile: { path: string; staged: boolean } | null;

  show(): void;
  hide(): void;
  setTab(tab: GitTab): void;
  refresh(): Promise<void>;
  loadMore(): Promise<void>;
  select(hash: string | null): Promise<void>;
  selectFile(path: string): Promise<void>;
  selectChange(path: string, staged: boolean): Promise<void>;
  /** 点分支/标签：定位到它指向的提交（还没加载到就继续往后加载，直到找到或没有更多） */
  reveal(branch: GitBranch): Promise<void>;
}

let seq = 0; // 提交详情请求序号
let diffSeq = 0;

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

export const useGit = create<GitState>((set, get) => ({
  open: false,
  tab: "history",
  loading: false,
  error: "",
  overview: null,
  commits: [],
  hasMore: false,
  loadingMore: false,
  selected: null,
  detail: null,
  detailError: "",
  file: null,
  diff: null,
  diffLoading: false,
  diffError: "",
  changeFile: null,

  show() {
    set({ open: true });
    void get().refresh();
  },
  hide() {
    set({ open: false });
  },
  setTab(tab) {
    set({ tab });
  },

  async refresh() {
    set({ loading: true, error: "" });
    try {
      const [overview, commits] = await Promise.all([backend().gitOverview(), backend().gitLog(0, PAGE)]);
      set({ overview, commits, hasMore: commits.length === PAGE, loading: false });
      // 保持选中；没有选中或选中的提交已不存在（例如被 rebase 掉了）时选 HEAD
      const sel = get().selected;
      const want = sel && commits.some((c) => c.hash === sel) ? sel : (overview.status.head || commits[0]?.hash || null);
      if (want) await get().select(want);
      else set({ selected: null, detail: null });
    } catch (e) {
      set({ loading: false, error: msg(e), overview: null, commits: [], hasMore: false });
    }
  },

  async loadMore() {
    const { loadingMore, hasMore, commits } = get();
    if (loadingMore || !hasMore) return;
    set({ loadingMore: true });
    try {
      const more = await backend().gitLog(commits.length, PAGE);
      // 加载期间刷新过（列表被重置）就丢弃这一页
      if (get().commits.length !== commits.length) return set({ loadingMore: false });
      set({ commits: [...commits, ...more], hasMore: more.length === PAGE, loadingMore: false });
    } catch (e) {
      set({ loadingMore: false, error: msg(e) });
    }
  },

  async select(hash) {
    const my = ++seq;
    if (!hash) return set({ selected: null, detail: null, file: null, diff: null });
    set({ selected: hash, detailError: "", file: null, diff: null, diffError: "" });
    try {
      const detail = await backend().gitCommit(hash);
      if (my !== seq) return;
      set({ detail });
      // 默认展开第一个文件的 diff
      if (detail.files.length > 0) await get().selectFile(detail.files[0].path);
    } catch (e) {
      if (my === seq) set({ detail: null, detailError: msg(e) });
    }
  },

  async selectFile(path) {
    const hash = get().selected;
    if (!hash) return;
    const my = ++diffSeq;
    set({ file: path, diffLoading: true, diffError: "", diff: null });
    try {
      const diff = await backend().gitCommitDiff(hash, path);
      if (my === diffSeq) set({ diff, diffLoading: false });
    } catch (e) {
      if (my === diffSeq) set({ diffLoading: false, diffError: msg(e) });
    }
  },

  async selectChange(path, staged) {
    const my = ++diffSeq;
    set({ changeFile: { path, staged }, diffLoading: true, diffError: "", diff: null });
    try {
      const diff = await backend().gitWorkingDiff(path, staged);
      if (my === diffSeq) set({ diff, diffLoading: false });
    } catch (e) {
      if (my === diffSeq) set({ diffLoading: false, diffError: msg(e) });
    }
  },

  async reveal(branch) {
    set({ tab: "history" });
    for (let i = 0; i < 20; i++) {
      const { commits, hasMore } = get();
      if (commits.some((c) => c.hash === branch.hash)) return get().select(branch.hash);
      if (!hasMore) return;
      await get().loadMore();
    }
  },
}));
