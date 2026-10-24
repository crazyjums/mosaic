// 浏览器演示桥接：没有 Go 后端时（例如单独运行 `npm run dev`）使用。
//
// 它回放 demo-events.jsonl —— 这份文件是用真实 CLI 录下来的事件流：
//   go run ./cmd/cli -json -yes -no-store -p "帮我做个演示" > frontend/src/bridge/demo-events.jsonl
// （Makefile 里的 `make demo-events` 就是这条命令）。
// 因为数据来自真实内核，前端在浏览器里看到的和在桌面端看到的是同一套协议、同一种节奏。
//
// 为了让演示可交互，回放时做了三处改写：
//   1. 用户消息替换成实际输入的文字；
//   2. 文本增量重新切成小片段，带延迟播放，模拟流式效果；
//   3. 遇到 permission_request 时真的停下来等用户点击；用户拒绝时，把对应的 tool_result 改成失败。

import type { AgentEvent, Decision, Usage } from "../protocol/events";
import type { Backend, ProfileInfo, SessionSummary, Status } from "./types";
import raw from "./demo-events.jsonl?raw";

const recorded: AgentEvent[] = raw
  .split("\n")
  .filter((l) => l.trim() !== "")
  .map((l) => JSON.parse(l) as AgentEvent);

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const rid = (prefix: string) => `${prefix}_${Math.random().toString(16).slice(2, 10)}`;
const zeroUsage = (): Usage => ({ input_tokens: 0, output_tokens: 0 });

interface DemoSession {
  summary: SessionSummary;
  events: AgentEvent[];
  usage: Usage;
}

export function createDemoBackend(): Backend {
  const listeners = new Set<(ev: AgentEvent) => void>();
  const sessions = new Map<string, DemoSession>();
  let current = newSession();
  let running = false;
  let cancelled = false;
  let seq = 0;
  let model = "demo";
  let pending: { requestId: string; resolve: (d: Decision) => void } | null = null;

  function newSession(): DemoSession {
    const now = new Date().toISOString();
    return {
      summary: { id: rid("sess"), title: "", provider: "fake", model: "demo", message_count: 0, created_at: now, updated_at: now },
      events: [],
      usage: zeroUsage(),
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
      provider: "fake",
      model,
      usage: current.usage,
      running,
      workspace: "/demo/workspace（浏览器演示）",
      pending_permissions: pending ? [pending.requestId] : [],
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
    const denied = new Set<string>();

    for (const original of recorded) {
      if (cancelled) return endTurn("cancelled");
      const ev = structuredClone(original) as AgentEvent;
      if (ev.message_id) ev.message_id = remap(ev.message_id);
      if ("call_id" in ev.data) ev.data.call_id = remap(ev.data.call_id);

      switch (ev.type) {
        case "user_message":
          ev.data.text = text;
          break;
        case "text_delta":
        case "thinking_delta":
          await stream(ev, ev.data.text);
          continue;
        case "permission_resolved":
          continue; // 由用户的真实点击生成，见下面的 permission_request
        case "permission_request": {
          const requestId = rid("perm");
          ev.data.request_id = requestId;
          emit(ev);
          const decision = await new Promise<Decision>((resolve) => (pending = { requestId, resolve }));
          pending = null;
          emit({ ...ev, type: "permission_resolved", data: { request_id: requestId, call_id: ev.data.call_id, decision } });
          if (decision === "deny") denied.add(ev.data.call_id);
          continue;
        }
        case "tool_result":
          if (denied.has(ev.data.call_id)) {
            ev.data.output = "permission denied by user";
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

  const profiles: ProfileInfo[] = [
    { name: "fake", type: "fake", models: ["demo", "echo"], default_model: "demo", has_api_key: false, available: true },
    { name: "anthropic", type: "anthropic", models: ["claude-opus-5-5"], default_model: "claude-opus-5-5", has_api_key: false, available: false, error: "浏览器演示模式下不可用" },
  ];

  return {
    kind: "demo",
    protocolVersion: async () => 1,
    getStatus: async () => status(),
    getProfiles: async () => profiles,
    listSessions: async () =>
      [...sessions.values()].map((s) => s.summary).sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
    newSession: async () => {
      if (running) throw new Error("engine: a turn is already running");
      current = newSession();
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
    deleteSession: async (id) => void sessions.delete(id),
    setModel: async (_profile, m) => {
      model = m || "demo";
      return status();
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
    onEvent(cb) {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
  };
}
