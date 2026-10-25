// 状态栏配置：显示哪些项、按什么顺序、怎么显示。
//
// 配置的结构完全由前端定义；后端（internal/settings）只把它当作一段 JSON 原样存进 settings.json。
// 所以新增一个状态栏项只需要：在 ITEMS 里加一条 → 在 StatusBar.tsx 里写它的渲染。

import { create } from "zustand";
import { backend } from "../bridge";

export type ItemId = "model" | "tokens" | "context" | "cost" | "chatgpt" | "turn" | "ttft" | "workspace" | "git" | "permission" | "running" | "script";

export interface ItemMeta {
  label: string;
  description: string;
  /** 新增到配置里时默认是否开启 */
  defaultEnabled: boolean;
}

/** 所有状态栏项，顺序即默认顺序。 */
export const ITEMS: Record<ItemId, ItemMeta> = {
  model: { label: "厂商 / 模型", description: "当前使用的厂商和模型，可以直接点击切换", defaultEnabled: true },
  tokens: { label: "Token 用量", description: "本会话累计的输入 / 输出 token", defaultEnabled: true },
  context: { label: "上下文占用", description: "最近一次请求的上下文大小；配置了模型的上下文窗口时显示占比和进度条", defaultEnabled: true },
  cost: { label: "预估花费", description: "按模型单价估算的本会话花费；没配置单价的模型不显示", defaultEnabled: true },
  chatgpt: {
    label: "ChatGPT 额度",
    description: "使用 ChatGPT 账号时显示：本应用最近 5 小时 / 7 天用掉的 token、额度是否已用完；点击打开 ChatGPT 的用量页面查看剩余额度",
    defaultEnabled: true,
  },
  turn: { label: "本轮耗时 / 步数", description: "运行中实时显示已用时间和第几步；空闲时显示上一轮耗时", defaultEnabled: true },
  ttft: { label: "首字延迟", description: "最近一次模型调用从发出请求到收到第一个字（含思考）的时间；悬停查看本会话平均值", defaultEnabled: true },
  workspace: { label: "工作区", description: "工具读写文件的根目录", defaultEnabled: true },
  git: { label: "Git 分支", description: "工作区是 git 仓库时显示分支，以及未提交改动的文件数", defaultEnabled: true },
  permission: { label: "权限模式", description: "只读 / 默认 / 自动接受编辑 / 全部放行，可直接切换", defaultEnabled: true },
  running: { label: "运行状态", description: "空闲 / 运行中 / 等待确认", defaultEnabled: true },
  script: { label: "脚本输出", description: "自定义命令输出的第一行（在下方“自定义脚本”中设置；没有设置时不显示）", defaultEnabled: true },
};

export const ITEM_IDS = Object.keys(ITEMS) as ItemId[];

export interface StatusLineConfig {
  version: 1;
  items: { id: ItemId; enabled: boolean }[];
  /** 工作区显示目录名还是完整路径 */
  workspaceStyle: "name" | "full";
  /** Token 用量中是否显示缓存命中 */
  showCache: boolean;
}

export const DEFAULT_CONFIG: StatusLineConfig = {
  version: 1,
  items: ITEM_IDS.map((id) => ({ id, enabled: ITEMS[id].defaultEnabled })),
  workspaceStyle: "name",
  showCache: true,
};

/**
 * 把保存的配置（可能来自旧版本、可能被手改过）规范化成当前版本的完整配置：
 *   - 保留已知项的顺序和开关；丢弃不认识的项；
 *   - 新版本新增的项追加到末尾，使用默认开关——这样升级后用户不会丢掉自己的设置，也能看到新功能。
 */
export function normalizeConfig(raw: unknown): StatusLineConfig {
  const r = (raw && typeof raw === "object" ? raw : {}) as Partial<StatusLineConfig>;
  const seen = new Set<ItemId>();
  const items: StatusLineConfig["items"] = [];
  for (const it of Array.isArray(r.items) ? r.items : []) {
    if (it && ITEM_IDS.includes(it.id) && !seen.has(it.id)) {
      seen.add(it.id);
      items.push({ id: it.id, enabled: !!it.enabled });
    }
  }
  for (const id of ITEM_IDS) if (!seen.has(id)) items.push({ id, enabled: ITEMS[id].defaultEnabled });
  return {
    version: 1,
    items,
    workspaceStyle: r.workspaceStyle === "full" ? "full" : "name",
    showCache: r.showCache ?? DEFAULT_CONFIG.showCache,
  };
}

interface StatusLineStore {
  config: StatusLineConfig;
  /** 自定义状态栏命令（单独保存，见 Go 端 settings.File.StatusLineScript） */
  script: string;
  load(): Promise<void>;
  /** 修改并立即保存 */
  update(fn: (c: StatusLineConfig) => StatusLineConfig): Promise<void>;
  reset(): Promise<void>;
  saveScript(cmd: string): Promise<void>;
}

export const useStatusLine = create<StatusLineStore>((set, get) => ({
  config: DEFAULT_CONFIG,
  script: "",

  async load() {
    try {
      const [cfg, script] = await Promise.all([backend().getStatusLineConfig(), backend().getStatusLineScript()]);
      set({ config: normalizeConfig(cfg), script });
    } catch {
      // 读不到就用默认配置，状态栏不应该因此显示不出来。
    }
  },

  async saveScript(cmd) {
    await backend().setStatusLineScript(cmd);
    set({ script: cmd.trim() });
  },

  async update(fn) {
    const next = fn(get().config);
    set({ config: next }); // 先更新界面，再保存
    await backend().saveStatusLineConfig(next);
  },

  reset: () => get().update(() => DEFAULT_CONFIG),
}));
