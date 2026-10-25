// 浏览器演示桥接：没有 Go 后端时（例如单独运行 `npm run dev`）使用。
//
// 它回放 demo-events.jsonl —— 这份文件是用真实 CLI 录下来的事件流：
//   go run ./cmd/cli -demo -json -yes -no-store -p "帮我做个演示" > frontend/src/bridge/demo-events.jsonl
// （Makefile 里的 `make demo-events` 就是这条命令）。
// 因为数据来自真实内核，前端在浏览器里看到的和在桌面端看到的是同一套协议、同一种节奏。
//
// 为了让演示可交互，回放时做了三处改写：
//   1. 用户消息替换成实际输入的文字；
//   2. 文本增量重新切成小片段，带延迟播放，模拟流式效果；
//   3. 遇到 permission_request 时按当前权限档位处理（与 Go 端 permission.ModePolicy 一致）：
//      需要询问时真的停下来等用户点击；被拒绝时，把对应的 tool_result 改成失败。

import type { AgentEvent, Decision, Usage } from "../protocol/events";
import type {
  Backend, General, GeneralView, GroupView, MemberState, UsageNode, UsageReport, UsageRequest, UsageTotals, MCPServerConfig, PermissionMode, PermissionRule, ProfileInfo, ProviderView, SessionSummary, Status, ToolInfo,
} from "./types";
import raw from "./demo-events.jsonl?raw";

const recorded: AgentEvent[] = raw
  .split("\n")
  .filter((l) => l.trim() !== "")
  .map((l) => JSON.parse(l) as AgentEvent);

// 录制时使用的用户输入。回放时把它出现的所有地方（思考、工具参数）替换成用户实际输入的文字。
const recordedPrompt = recorded.find((e) => e.type === "user_message")?.data.text ?? "";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const rid = (prefix: string) => `${prefix}_${Math.random().toString(16).slice(2, 10)}`;
const zeroUsage = (): Usage => ({ input_tokens: 0, output_tokens: 0 });

interface DemoSession {
  summary: SessionSummary;
  events: AgentEvent[];
  usage: Usage;
  /** 会话的工作区；为空表示跟随默认工作区 */
  workspace: string;
}

/** 演示模式的用量数据：近几天的示意记录（美元）。 */
function demoUsageReport(req: UsageRequest): UsageReport {
  const today = new Date();
  const day = (back: number) => new Date(today.getTime() - back * 86400_000).toISOString().slice(0, 10);
  const mk = (back: number, profile: string, model: string, input: number, output: number, cr: number, requests: number, price?: [number, number]) => ({ day: day(back), profile, model, input, output, cr, requests, price });
  const rows = [
    mk(0, "anthropic", "claude-sonnet", 860_000, 98_000, 520_000, 121, [3, 15]),
    mk(0, "volcengine", "deepseek-v4", 310_000, 31_000, 120_000, 63),
    mk(0, "chatgpt", "gpt-6-luna", 30_000, 5_000, 0, 2),
    mk(1, "anthropic", "claude-sonnet", 970_000, 101_000, 610_000, 142, [3, 15]),
    mk(2, "anthropic", "claude-sonnet", 670_000, 77_000, 400_000, 90, [3, 15]),
    mk(4, "volcengine", "deepseek-v4", 410_000, 52_000, 150_000, 71),
  ];
  const days = req.range === "today" ? 0 : req.range === "7d" ? 6 : req.range === "30d" ? 29 : 365;
  const inRange = rows.filter((r) => r.day >= day2(days));
  function day2(back: number) {
    return new Date(today.getTime() - back * 86400_000).toISOString().slice(0, 10);
  }
  const zero = (): UsageTotals => ({ input: 0, output: 0, cache_read: 0, cache_write: 0, requests: 0, failed_tries: 0, handed_on: 0, cost_usd: 0, unpriced_calls: 0, plan_calls: 0, cache_hit: 0 });
  const fin = (t: UsageTotals) => ({ ...t, cache_hit: t.input ? t.cache_read / t.input : 0 });
  const leafOf = (r: (typeof rows)[number]): UsageNode => {
    const t = zero();
    add2(t, r);
    return { key: `${r.profile}/${r.model}`, label: `${r.profile} / ${r.model}`, priced: !!r.price, plan: r.profile === "chatgpt", ...fin(t) };
  };
  function add2(t: UsageTotals, r: (typeof rows)[number]) {
    t.input += r.input; t.output += r.output; t.cache_read += r.cr; t.requests += r.requests;
    if (r.price) t.cost_usd += ((r.input - r.cr) * r.price[0] + r.cr * r.price[0] * 0.1 + r.output * r.price[1]) / 1e6;
    else if (r.profile === "chatgpt") t.plan_calls += r.requests;
    else t.unpriced_calls += r.requests;
  }
  const totals = zero();
  inRange.forEach((r) => add2(totals, r));
  const merge = (a: UsageTotals, b: UsageTotals) => {
    for (const k of Object.keys(a) as (keyof UsageTotals)[]) if (k !== "cache_hit") a[k] += b[k];
  };
  const groupBy = (keyOf: (r: (typeof rows)[number]) => string, label: (k: string) => string): UsageNode[] => {
    const m = new Map<string, UsageNode>();
    for (const r of inRange) {
      const k = keyOf(r);
      const leaf = leafOf(r);
      const n = m.get(k) ?? { key: k, label: label(k), priced: true, ...zero(), children: [] };
      merge(n, leaf);
      n.children!.push(leaf);
      m.set(k, n);
    }
    return [...m.values()].map((n) => ({ ...n, ...fin(n) }));
  };
  const nodes = req.by === "model" ? inRange.map(leafOf) : req.by === "profile" ? groupBy((r) => r.profile, (k) => k) : groupBy((r) => r.day, (k) => k).sort((a, b) => b.key.localeCompare(a.key));
  return {
    by: req.by, totals: fin(totals), nodes, unpriced_models: ["volcengine/deepseek-v4"],
    from: day2(days), to: day(0), profiles: ["anthropic", "chatgpt", "volcengine"],
  };
}

export function createDemoBackend(): Backend {
  const listeners = new Set<(ev: AgentEvent) => void>();
  const sessions = new Map<string, DemoSession>();
  let current = newSession();
  let running = false;
  let cancelled = false;
  let seq = 0;
  let provider = "fake";
  let permissionMode: PermissionMode = "default";
  let statusLineConfig: unknown = null;
  let uiPrefs: unknown = null;
  let statusLineScript = "";
  let rules: PermissionRule[] = [];
  let mcpServers: MCPServerConfig[] = [];
  let groups: GroupView[] = [];
  let demoProxy = "";
  let disabledTools: string[] = [];
  const demoTools: Omit<ToolInfo, "disabled">[] = [
    { name: "read_file", description: "Read a text file in the workspace.", risk: "read" },
    { name: "list_dir", description: "List a directory in the workspace.", risk: "read" },
    { name: "write_file", description: "Create or overwrite a file in the workspace.", risk: "write" },
    { name: "shell", description: "Run a shell command in the workspace.", risk: "exec" },
  ];
  let general: General = {};
  const demoWorkspace = "/demo/workspace";
  const generalView = (): GeneralView => ({
    saved: general,
    effective: {
      workspace: general.workspace || demoWorkspace,
      max_steps: general.max_steps || 20,
      max_tokens: general.max_tokens || 0,
      shell_timeout_seconds: general.shell_timeout_seconds || 60,
      system_prompt: general.system_prompt || "",
    },
    defaults: { workspace: demoWorkspace, max_steps: 20, max_tokens: 0, shell_timeout_seconds: 60 },
    default_system_prompt:
      `You are a helpful coding agent running inside a desktop client.\n\nEnvironment:\n- Workspace root: ${general.workspace || demoWorkspace} (all file paths are relative to it)\n- OS: browser/demo\n\nGuidelines:\n- ...（浏览器演示模式下的示意）`,
    workspace_locked: false,
  });
  let model = "demo";
  let pending: { requestId: string; resolve: (d: Decision) => void } | null = null;

  /** 新会话沿用传入的工作区（与真实后端一致：沿用当前会话的工作区）。 */
  function newSession(workspace = ""): DemoSession {
    const now = new Date().toISOString();
    return {
      summary: { id: rid("sess"), title: "", provider: "fake", model: "demo", workspace, message_count: 0, created_at: now, updated_at: now },
      events: [],
      usage: zeroUsage(),
      workspace,
    };
  }

  function emit(ev: AgentEvent) {
    const full = { ...ev, seq: ++seq, id: rid("ev"), session_id: current.summary.id, ts: new Date().toISOString() } as AgentEvent;
    current.events.push(full);
    listeners.forEach((cb) => cb(full));
  }

  function status(): Status {
    return {
      session_id: current.summary.id,
      title: current.summary.title,
      provider,
      model,
      usage: current.usage,
      running,
      workspace: current.workspace || general.workspace || demoWorkspace,
      workspace_is_default: !current.workspace,
      pending_permissions: pending ? [pending.requestId] : [],
      permission_mode: permissionMode,
    };
  }

  /** 回放录制的 demo 剧本。 */
  async function play(text: string) {
    // 每一轮重新生成 message_id / call_id，避免多轮回放时 ID 冲突。
    const ids = new Map<string, string>();
    const remap = (id: string) => {
      if (!ids.has(id)) ids.set(id, rid(id.split("_")[0]));
      return ids.get(id)!;
    };
    const denied = new Map<string, string>(); // call_id → 失败原因
    const escaped = JSON.stringify(text).slice(1, -1); // 作为 JSON 字符串内容插入，需要转义

    for (const original of recorded) {
      if (cancelled) return endTurn("cancelled");
      const json = JSON.stringify(original);
      const ev = JSON.parse(recordedPrompt ? json.replaceAll(recordedPrompt, escaped) : json) as AgentEvent;
      if (ev.message_id) ev.message_id = remap(ev.message_id);
      if ("call_id" in ev.data) ev.data.call_id = remap(ev.data.call_id);

      switch (ev.type) {
        case "text_delta":
        case "thinking_delta":
          await stream(ev, ev.data.text);
          continue;
        case "permission_resolved":
          continue; // 由用户的真实点击生成，见下面的 permission_request
        case "permission_request": {
          // 与 ModePolicy.Evaluate 相同的规则：不需要询问时，真实内核不会发出权限事件。
          const risk = ev.data.risk;
          if (permissionMode === "readonly") {
            denied.set(ev.data.call_id, `permission denied by policy: ${risk} tools are not allowed in the current mode`);
            continue;
          }
          if (permissionMode === "auto" || (permissionMode === "accept_edits" && risk === "write")) continue;
          const requestId = rid("perm");
          ev.data.request_id = requestId;
          emit(ev);
          const decision = await new Promise<Decision>((resolve) => (pending = { requestId, resolve }));
          pending = null;
          emit({ ...ev, type: "permission_resolved", data: { request_id: requestId, call_id: ev.data.call_id, decision } });
          if (decision === "deny") denied.set(ev.data.call_id, "permission denied by user");
          continue;
        }
        case "tool_result":
          if (denied.has(ev.data.call_id)) {
            ev.data.output = denied.get(ev.data.call_id)!;
            ev.data.is_error = true;
          }
          break;
        case "turn_end":
          addUsage(ev.data.usage);
          ev.data.session_usage = current.usage;
          break;
      }
      emit(ev);
      await sleep(ev.type === "tool_result" ? 250 : 60);
    }
  }

  /** "echo" 模型：复述用户输入。 */
  async function playEcho(text: string) {
    const base = { v: 1, id: "", seq: 0, session_id: "", agent_id: "main", ts: "" };
    const message_id = rid("msg");
    emit({ ...base, type: "turn_start", data: { turn_id: rid("turn"), provider: "fake", model } });
    emit({ ...base, type: "user_message", data: { text } });
    emit({ ...base, message_id, type: "message_start", data: { provider: "fake", model, step: 1 } });
    await stream({ ...base, message_id, type: "text_delta", data: { text: "" } }, `（fake）你说的是：${text}`);
    const usage = { input_tokens: 40, output_tokens: 12 };
    emit({ ...base, message_id, type: "message_end", data: { stop_reason: "end_turn", usage } });
    addUsage(usage);
    endTurn("completed", usage);
  }

  /** 把一段文本切成小片段逐个发出，模拟流式输出。 */
  async function stream(ev: AgentEvent & { type: "text_delta" | "thinking_delta" }, text: string) {
    for (const chunk of text.match(/[\s\S]{1,3}/g) ?? []) {
      if (cancelled) return;
      emit({ ...ev, data: { ...ev.data, text: chunk } });
      await sleep(18);
    }
  }

  function addUsage(u: Usage) {
    current.usage = {
      input_tokens: current.usage.input_tokens + u.input_tokens,
      output_tokens: current.usage.output_tokens + u.output_tokens,
    };
  }

  function endTurn(reason: "completed" | "cancelled", usage: Usage = zeroUsage()) {
    emit({
      v: 1, id: "", seq: 0, session_id: "", agent_id: "main", ts: "",
      type: "turn_end",
      data: { turn_id: "", reason, steps: 1, usage, session_usage: current.usage },
    });
  }

  // 设置页的数据也放在内存里：可以添加、编辑、删除，但无法真正连接厂商（浏览器里没有后端）。
  let providers: ProviderView[] = [
    {
      name: "fake", type: "fake", base_url: "", models: ["demo", "echo"], default_model: "demo",
      options: { models_meta: { demo: { context_window: 200000, input_price: 3, output_price: 15 } } },
      has_api_key: false, api_key_hint: "", source: "config", deletable: false, available: true,
    },
    {
      name: "anthropic", type: "anthropic", base_url: "", models: ["claude-opus-5-5"], default_model: "claude-opus-5-5",
      options: null, has_api_key: false, api_key_hint: "", source: "config", deletable: false, available: false,
      error: "missing API key (set api_key_env, e.g. ANTHROPIC_API_KEY)",
    },
  ];
  const profiles = (): ProfileInfo[] =>
    providers.map((p) => ({
      name: p.name, type: p.type, models: p.models, default_model: p.default_model,
      has_api_key: p.has_api_key, available: p.available, error: p.error,
      model_meta: (p.options?.models_meta as ProfileInfo["model_meta"]) ?? null,
    }));
  const demoOnly = "浏览器演示模式没有连接 Go 后端，无法访问真实厂商。请在桌面端（wails dev / 打包后的应用）中使用。";

  return {
    kind: "demo",
    protocolVersion: async () => 1,
    getStatus: async () => status(),
    getProfiles: async () => profiles(),
    listSessions: async () =>
      [...sessions.values()].map((s) => s.summary).sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
    newSession: async () => {
      if (running) throw new Error("engine: a turn is already running");
      current = newSession(current.workspace);
      return status();
    },
    loadSession: async (id) => {
      const s = sessions.get(id);
      if (!s) throw new Error("session not found");
      current = s;
      // 演示模式下直接重放保存的事件；真实后端会把历史消息转换成事件（core.ReplayEvents）。
      s.events.forEach((ev) => listeners.forEach((cb) => cb(ev)));
      return status();
    },
    deleteSession: async (id) => {
      // 与真实后端一致：删除当前会话时切换到新的空会话，运行中不允许删除当前会话。
      if (id === current.summary.id) {
        if (running) throw new Error("engine: a turn is already running");
        current = newSession(current.workspace);
      }
      sessions.delete(id);
    },
    renameSession: async (id, title) => {
      const s = sessions.get(id) ?? (id === current.summary.id ? current : undefined);
      if (!s) throw new Error("session not found");
      if (!title.trim()) throw new Error("标题不能为空");
      s.summary.title = title.trim().slice(0, 100);
      const ev = { type: "session_updated", data: { title: s.summary.title }, seq: ++seq, id: rid("ev"), session_id: id, agent_id: "main", ts: new Date().toISOString(), v: 1 } as unknown as AgentEvent;
      listeners.forEach((cb) => cb(ev));
    },
    openExternalURL: async (url) => {
      window.open(url, "_blank", "noopener");
    },
    readWorkspaceImage: async () => {
      throw new Error("浏览器演示模式不能读取本地文件");
    },
    setModel: async (p, m) => {
      provider = p || "fake";
      model = m || "demo";
      return status();
    },
    listProviders: async () => providers,
    saveProvider: async (input) => {
      if (!input.name.trim()) throw new Error("请填写名称");
      if (input.models.length === 0) throw new Error("请至少填写一个模型");
      const old = providers.find((p) => p.name === (input.original_name || input.name));
      const key = input.api_key || "";
      const view: ProviderView = {
        name: input.name, type: input.type, base_url: input.base_url, models: input.models,
        default_model: input.default_model || input.models[0], options: input.options,
        has_api_key: !!key || !!old?.has_api_key,
        api_key_hint: key ? `${key.slice(0, 3)}…${key.slice(-4)}` : (old?.api_key_hint ?? ""),
        source: old?.source === "config" ? "override" : (old?.source ?? "settings"),
        deletable: true, available: true,
      };
      providers = providers.filter((p) => p.name !== input.original_name && p.name !== input.name).concat(view);
    },
    deleteProvider: async (name) => {
      providers = providers.filter((p) => p.name !== name);
    },
    getGeneral: async () => generalView(),
    saveGeneral: async (g) => {
      if (running) throw new Error("engine: a turn is already running");
      general = g;
      return generalView();
    },
    setSessionWorkspace: async (dir) => {
      if (running) throw new Error("engine: a turn is already running");
      current.workspace = dir;
      current.summary.workspace = dir;
      return status();
    },
    chooseDirectory: async (start) => window.prompt("浏览器演示模式没有文件夹对话框，请直接输入路径：", start) ?? "",
    getDataInfo: async () => ({
      data_dir: "~/.myllmapp（演示）", settings_path: "~/.myllmapp/settings.json", db_path: "~/.myllmapp/sessions.db",
      db_bytes: 0, sessions: sessions.size,
    }),
    openDataDir: async () => {
      throw new Error(demoOnly);
    },
    clearSessions: async () => {
      if (running) throw new Error("engine: a turn is already running");
      const n = sessions.size;
      sessions.clear();
      current = newSession(current.workspace);
      return n;
    },
    getWorkspaceInfo: async () => ({
      path: "/demo/workspace", name: "workspace", is_repo: true, branch: "main", dirty: true, changed_files: 2,
    }),
    setPermissionMode: async (mode) => {
      permissionMode = mode;
      return status();
    },
    getStatusLineConfig: async () => statusLineConfig,
    saveStatusLineConfig: async (cfg) => {
      statusLineConfig = cfg;
    },
    testProvider: async (input) => ({ ok: false, model: input.default_model, latency_ms: 0, error: demoOnly }),
    fetchModels: async () => {
      throw new Error(demoOnly);
    },
    send: async (text) => {
      if (running) throw new Error("engine: a turn is already running");
      running = true;
      cancelled = false;
      if (!current.summary.title) current.summary.title = text.slice(0, 40);
      sessions.set(current.summary.id, current);
      (model === "echo" ? playEcho(text) : play(text)).finally(() => {
        running = false;
        current.summary.updated_at = new Date().toISOString();
        current.summary.message_count += 1;
      });
    },
    cancel: async () => {
      cancelled = true;
      pending?.resolve("deny");
    },
    respondPermission: async (id, decision) => {
      if (pending?.requestId === id) pending.resolve(decision);
    },
    getChatGPT: async () => ({ state: "logged_out", registered: false, error: demoOnly }),
    loginChatGPT: async () => {
      throw new Error(demoOnly);
    },
    cancelChatGPTLogin: async () => {},
    logoutChatGPT: async () => {},
    getChatGPTUsage: async () => {
      const w = { requests: 0, input_tokens: 0, output_tokens: 0, cached_tokens: 0 };
      return { logged_in: false, last_5h: w, last_7d: w, usage_url: "https://chatgpt.com/settings/usage", quota_enabled: false };
    },
    setCodexQuota: async () => {
      throw new Error(demoOnly);
    },
    // 演示模式的用量、模型组和代理：内存里的示意数据，刷新页面就没了。
    getUsageReport: async (req) => demoUsageReport(req),
    saveUsageCSV: async () => {
      throw new Error(demoOnly);
    },
    getGroups: async () => groups,
    saveGroup: async (input) => {
      if (!input.name.trim()) throw new Error("请填写模型组名称");
      if (input.members.length === 0) throw new Error("模型组至少需要一个成员");
      const { original_name, ...cfg } = input;
      groups = groups.filter((g) => g.name !== original_name && g.name !== cfg.name).concat({ ...cfg, available: true });
    },
    deleteGroup: async (name) => {
      groups = groups.filter((g) => g.name !== name);
    },
    getGroupStatuses: async () =>
      groups.map((g) => ({
        name: g.name,
        strategy: g.strategy,
        members: g.members.map((m, i) => ({ ...m, state: (i === 1 ? "cooling" : "healthy") as MemberState, until: i === 1 ? Date.now() + 95_000 : undefined, kind: i === 1 ? "rate_limit" : undefined, message: i === 1 ? "HTTP 429: rate limited" : undefined })),
      })),
    resetGroupMember: async () => {},
    getProxy: async () => ({ set: demoProxy !== "", display: demoProxy }),
    setProxy: async (raw) => {
      demoProxy = raw.trim();
    },
    getVersion: async () => "dev",
    checkUpdate: async () => ({ current: "dev", latest: "", available: false, manual: false, disabled: "浏览器演示模式不检查更新" }),
    getVersionHistory: async () => [
      { version: "1.1.0", date: "2026-10-25", summary: "更新提示现在会告诉你这次改了什么。", sections: [{ kind: "added" as const, items: ["发现新版本时可以查看更新日志"] }] },
      { version: "1.0.1", date: "2026-10-25", summary: "第一个正式发布的版本。", sections: [{ kind: "added" as const, items: ["自动更新"] }, { kind: "fixed" as const, items: ["修复运行中无法打开设置的问题"] }] },
    ],
    installUpdate: async () => {
      throw new Error(demoOnly);
    },
    getUpdateStatus: async () => ({ state: "idle" as const, done: 0, total: 0 }),
    openChatGPTUsage: async () => {
      window.open("https://chatgpt.com/settings/usage", "_blank", "noopener");
    },
    // 演示模式没有真实的会话历史，按回放过的事件粗略统计。
    getContextBreakdown: async () => {
      const text = (t: string) => Math.ceil(t.length / 2);
      let user = 0, assistant = 0, thinking = 0, calls = 0, results = 0, nUser = 0, nAssistant = 0, nThink = 0, nCalls = 0, nResults = 0;
      for (const ev of current.events) {
        if (ev.type === "user_message") (user += text(ev.data.text)), nUser++;
        else if (ev.type === "text_delta") assistant += text(ev.data.text);
        else if (ev.type === "thinking_delta") thinking += text(ev.data.text);
        else if (ev.type === "message_start") nAssistant++, nThink++;
        else if (ev.type === "tool_call") (calls += text(JSON.stringify(ev.data.input))), nCalls++;
        else if (ev.type === "tool_result") (results += text(ev.data.output)), nResults++;
      }
      const parts = [
        { kind: "system" as const, count: 1, tokens: 180 },
        { kind: "tools" as const, count: demoTools.length, tokens: 420 },
        { kind: "user" as const, count: nUser, tokens: user },
        { kind: "assistant" as const, count: nAssistant, tokens: assistant },
        { kind: "thinking" as const, count: nThink, tokens: thinking },
        { kind: "tool_call" as const, count: nCalls, tokens: calls },
        { kind: "tool_result" as const, count: nResults, tokens: results },
      ].filter((p) => p.count > 0);
      return { parts, largest: [], total: parts.reduce((a, p) => a + p.tokens, 0), messages: nUser + nAssistant, tool_count: demoTools.length };
    },
    // ---- 权限与工具、状态栏脚本、MCP、外观：演示模式下只保存在内存里 ----
    listTools: async () => demoTools.map((t) => ({ ...t, disabled: disabledTools.includes(t.name) })),
    setDisabledTools: async (names) => {
      disabledTools = names;
    },
    getPermissionRules: async () => rules,
    setPermissionRules: async (r) => {
      rules = r;
    },
    getStatusLineScript: async () => statusLineScript,
    setStatusLineScript: async (cmd) => {
      statusLineScript = cmd;
    },
    runStatusLine: async () => (statusLineScript ? "（演示）脚本输出" : ""),
    testStatusLineScript: async () => {
      throw new Error(demoOnly);
    },
    listMCPServers: async () => mcpServers,
    saveMCPServers: async (list) => {
      mcpServers = list;
    },
    getMCPStatus: async () =>
      mcpServers.map((c) => ({ name: c.name, state: c.disabled ? "disabled" : "failed", error: c.disabled ? undefined : demoOnly, tools: [] })),
    reconnectMCP: async () => {},
    getUIPrefs: async () => uiPrefs,
    saveUIPrefs: async (p) => {
      uiPrefs = p;
    },
    getKeychain: async () => ({ enabled: false, available: false, error: demoOnly }),
    setUseKeychain: async () => {
      throw new Error(demoOnly);
    },
    onEvent(cb) {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
  };
}
