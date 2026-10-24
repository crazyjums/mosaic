// 设置页的状态（Zustand）。和 chatStore 分开：设置不走事件流，是普通的"请求 → 响应"。
//
// 业务逻辑（校验、合并、持久化、热更新）都在 Go 端 internal/settings，这里只负责调用和保存界面状态。

import { create } from "zustand";
import { backend, type ProviderInput, type ProviderView, type TestResult } from "../bridge";
import { useChat } from "./chatStore";

interface SettingsStore {
  open: boolean;
  providers: ProviderView[];
  loading: boolean;
  error: string | null;

  show(): Promise<void>;
  hide(): void;
  reload(): Promise<void>;
  save(input: ProviderInput): Promise<boolean>;
  remove(name: string): Promise<boolean>;
  test(input: ProviderInput): Promise<TestResult>;
  fetchModels(input: ProviderInput): Promise<string[]>;
}

const message = (e: unknown) => String(e instanceof Error ? e.message : e);

export const useSettings = create<SettingsStore>((set, get) => ({
  open: false,
  providers: [],
  loading: false,
  error: null,

  async show() {
    set({ open: true, error: null });
    await get().reload();
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
}));
