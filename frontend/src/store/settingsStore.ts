// 设置页的状态（Zustand）。和 chatStore 分开：设置不走事件流，是普通的"请求 → 响应"。
//
// 业务逻辑（校验、合并、持久化、热更新）都在 Go 端 internal/settings，这里只负责调用和保存界面状态。

import { create } from "zustand";
import { backend, type DataInfo, type General, type GeneralView, type ProviderInput, type ProviderView, type TestResult } from "../bridge";
import { useChat } from "./chatStore";

export type SettingsTab = "general" | "providers" | "prompt" | "permissions" | "mcp" | "appearance" | "statusline" | "data" | "about";

interface SettingsStore {
  open: boolean;
  tab: SettingsTab;
  providers: ProviderView[];
  general: GeneralView | null;
  dataInfo: DataInfo | null;
  loading: boolean;
  error: string | null;

  show(tab?: SettingsTab): Promise<void>;
  setTab(tab: SettingsTab): void;
  hide(): void;
  reload(): Promise<void>;
  save(input: ProviderInput): Promise<boolean>;
  remove(name: string): Promise<boolean>;
  test(input: ProviderInput): Promise<TestResult>;
  fetchModels(input: ProviderInput): Promise<string[]>;

  loadGeneral(): Promise<void>;
  /** 保存常规设置；成功返回 true */
  saveGeneral(g: General): Promise<boolean>;
  loadDataInfo(): Promise<void>;
  openDataDir(): Promise<void>;
  /** 清空历史会话；返回删除的数量，失败返回 null */
  clearSessions(): Promise<number | null>;
  clearError(): void;
}

const message = (e: unknown) => String(e instanceof Error ? e.message : e);

export const useSettings = create<SettingsStore>((set, get) => ({
  open: false,
  tab: "general",
  providers: [],
  general: null,
  dataInfo: null,
  loading: false,
  error: null,

  async show(tab = "general") {
    set({ open: true, tab, error: null });
    await Promise.all([get().reload(), get().loadGeneral(), get().loadDataInfo()]);
  },
  setTab: (tab) => {
    set({ tab, error: null });
    if (tab === "data") void get().loadDataInfo();
  },
  hide: () => set({ open: false }),

  async reload() {
    set({ loading: true });
    try {
      set({ providers: await backend().listProviders(), error: null });
    } catch (e) {
      set({ error: message(e) });
    } finally {
      set({ loading: false });
    }
  },

  // 保存/删除后：刷新设置页列表，并让聊天界面刷新厂商下拉框和状态栏（保存可能自动切换了当前厂商）。
  async save(input) {
    try {
      await backend().saveProvider(input);
      await Promise.all([get().reload(), useChat.getState().refreshMeta()]);
      return true;
    } catch (e) {
      set({ error: message(e) });
      return false;
    }
  },

  async remove(name) {
    try {
      await backend().deleteProvider(name);
      await Promise.all([get().reload(), useChat.getState().refreshMeta()]);
      return true;
    } catch (e) {
      set({ error: message(e) });
      return false;
    }
  },

  async test(input) {
    try {
      return await backend().testProvider(input);
    } catch (e) {
      return { ok: false, model: input.default_model, latency_ms: 0, error: message(e) };
    }
  },

  fetchModels: (input) => backend().fetchModels(input),

  async loadGeneral() {
    try {
      set({ general: await backend().getGeneral() });
    } catch (e) {
      set({ error: message(e) });
    }
  },

  // 保存后刷新聊天界面：工作区变了，状态栏的路径和 git 信息都要更新。
  async saveGeneral(g) {
    try {
      set({ general: await backend().saveGeneral(g), error: null });
      const chat = useChat.getState();
      await Promise.all([chat.refreshMeta(), chat.refreshWorkspace()]);
      return true;
    } catch (e) {
      set({ error: message(e) });
      return false;
    }
  },

  async loadDataInfo() {
    try {
      set({ dataInfo: await backend().getDataInfo() });
    } catch (e) {
      set({ error: message(e) });
    }
  },

  async openDataDir() {
    try {
      await backend().openDataDir();
    } catch (e) {
      set({ error: message(e) });
    }
  },

  async clearSessions() {
    try {
      const n = await backend().clearSessions();
      await Promise.all([useChat.getState().resetToCurrentSession(), get().loadDataInfo()]);
      return n;
    } catch (e) {
      set({ error: message(e) });
      return null;
    }
  },

  clearError: () => set({ error: null }),
}));
