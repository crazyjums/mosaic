// 事件 → 界面状态的纯函数。
//
// 这是前端最核心、也最容易写错的部分，所以单独成文件、写成纯函数并配单元测试（reducer.test.ts）：
// 给定旧状态和一个事件，返回新状态，不做任何 I/O。
// zustand store（chatStore.ts）只是把它包了一层，组件只读状态、不解析事件。
//
// 状态的形状是为"渲染"服务的，而不是事件的简单堆积：
//   - timeline：按出现顺序排列的条目（用户消息、assistant 消息、错误、轮次分隔）；
//   - assistant 消息内部是"块"列表：思考 / 文本 / 图片 / 工具调用引用；
//   - 工具调用的详细状态放在 tools 字典里（按 call_id 索引），因为它会被后续多个事件
//     （permission_request、permission_resolved、tool_result）更新，而这些事件不一定带 message_id。

import type { AgentEvent, Decision, PayloadMap, Risk, TurnEndReason, Usage } from "../protocol/events";
import { PROTOCOL_VERSION } from "../protocol/events";

export type ToolStatus =
  | "queued" // 模型已发出调用，等待执行
  | "awaiting_permission" // 等待用户确认
  | "running" // 执行中
  | "done"
  | "error"
  | "denied";

export interface ToolView {
  callId: string;
  name: string;
  input: unknown;
  risk: Risk;
  status: ToolStatus;
  output?: string;
  durationMs?: number;
  permission?: { requestId: string; summary: string; decision?: Decision };
}

export type AssistantBlock =
  | { type: "thinking"; text: string; redacted: boolean }
  | { type: "text"; text: string }
  | { type: "image"; mediaType: string; data?: string; url?: string }
  | { type: "tool"; callId: string };

export interface AssistantItem {
  kind: "assistant";
  id: string; // message_id
  agentId: string;
  step: number;
  provider: string;
  model: string;
  blocks: AssistantBlock[];
  streaming: boolean;
  stopReason?: string;
  usage?: Usage;
  /** 发出请求（message_start）和收到第一个内容（思考 / 文本 / 工具调用 / 图片）的时间，用于计算首字延迟。重放的历史没有 */
  startedAt?: number;
  firstTokenAt?: number;
  /** 使用模型组时：实际应答的成员，以及前面被转交的尝试。provider / model 已经更新为实际应答者 */
  route?: RouteView;
  /** 这条消息所在的一轮的总耗时（毫秒），写在一轮里最后一条消息上，供执行摘要显示。重放的历史没有 */
  turnMs?: number;
}

export interface RouteView {
  group: string;
  tried: number;
  handedOn: { profile: string; model: string; kind: string; message: string }[];
}

export type TimelineItem =
  | { kind: "user"; id: string; text: string }
  | AssistantItem
  | { kind: "error"; id: string; code: string; message: string; fatal: boolean }
  | { kind: "notice"; id: string; text: string }
  | { kind: "turn_end"; id: string; reason: TurnEndReason; steps: number; usage: Usage; replay: boolean };

/** 预留：子 agent 树的节点。 */
export interface AgentNode {
  id: string;
  parentId: string;
  name: string;
  task: string;
  status: "running" | string;
}

/** 预留：后台任务。 */
export interface TaskView {
  id: string;
  title: string;
  status: "running" | string;
  log: string;
  exitCode?: number;
}

export interface ChatState {
  /** 当前显示的会话；其他会话的事件会被忽略。 */
  sessionId: string | null;
  timeline: TimelineItem[];
  tools: Record<string, ToolView>;
  /** 等待用户确认的请求（按到达顺序），弹窗显示第一个。 */
  permissions: PayloadMap["permission_request"][];
  running: boolean;
  provider: string;
  model: string;
  sessionUsage: Usage;
  agents: Record<string, AgentNode>;
  tasks: Record<string, TaskView>;
  lastSeq: number;
  versionWarning: boolean;
  /** 当前这一轮开始的时间（毫秒时间戳）；不在运行中时为 null。状态栏用它显示实时耗时。 */
  turnStartedAt: number | null;
  /** 上一轮的总耗时（毫秒） */
  lastTurnMs: number | null;
}

export const zeroUsage = (): Usage => ({ input_tokens: 0, output_tokens: 0 });

export function initialState(sessionId: string | null = null): ChatState {
  return {
    sessionId,
    timeline: [],
    tools: {},
    permissions: [],
    running: false,
    provider: "",
    model: "",
    sessionUsage: zeroUsage(),
    agents: {},
    tasks: {},
    lastSeq: 0,
    versionWarning: false,
    turnStartedAt: null,
    lastTurnMs: null,
  };
}

/** 依次应用一批事件（历史重放用）：调用方只需要 set 一次，界面只渲染一次。 */
export function foldEvents(state: ChatState, evs: AgentEvent[]): ChatState {
  return evs.reduce(applyEvent, state);
}

/** 把更早的一页历史放到已有内容前面。先在空状态上折叠成一段时间线，再拼接。 */
export function prependHistory(state: ChatState, evs: AgentEvent[]): ChatState {
  const frag = foldEvents(initialState(state.sessionId), evs);
  return { ...state, timeline: [...frag.timeline, ...state.timeline], tools: { ...frag.tools, ...state.tools } };
}

/** 应用一个事件，返回新状态（不修改旧状态）。 */
export function applyEvent(state: ChatState, ev: AgentEvent): ChatState {
  // 第 0 步：过滤。只处理当前会话的事件；尚未确定会话时采纳第一个事件的会话。
  if (state.sessionId && ev.session_id !== state.sessionId) return state;
  let s: ChatState = { ...state, sessionId: state.sessionId ?? ev.session_id, lastSeq: ev.seq };
  if (ev.v !== PROTOCOL_VERSION && !s.versionWarning) {
    s = {
      ...s,
      versionWarning: true,
      timeline: [...s.timeline, { kind: "notice", id: ev.id, text: `事件协议版本不一致：前端 v${PROTOCOL_VERSION}，后端 v${ev.v}，显示可能不完整。` }],
    };
  }

  switch (ev.type) {
    case "turn_start":
      return {
        ...s,
        running: !ev.data.replay,
        provider: ev.data.provider,
        model: ev.data.model,
        turnStartedAt: ev.data.replay ? s.turnStartedAt : Date.parse(ev.ts),
      };

    case "user_message":
      return { ...s, timeline: [...s.timeline, { kind: "user", id: ev.id, text: ev.data.text }] };

    case "message_start": {
      const item: AssistantItem = {
        kind: "assistant",
        id: ev.message_id ?? ev.id,
        agentId: ev.agent_id,
        step: ev.data.step,
        provider: ev.data.provider,
        model: ev.data.model,
        blocks: [],
        streaming: true,
        startedAt: s.running ? Date.parse(ev.ts) : undefined,
      };
      return { ...s, provider: ev.data.provider, model: ev.data.model, timeline: [...s.timeline, item] };
    }

    case "route":
      return updateMessage(s, ev.message_id, (m) => ({
        ...m,
        provider: ev.data.profile,
        model: ev.data.model,
        route: { group: ev.data.group, tried: ev.data.try, handedOn: ev.data.handed_on ?? [] },
      }));

    case "thinking_delta":
      return updateMessage(s, ev.message_id, (m) => ({
        ...firstToken(m, ev.ts),
        blocks: appendText(m.blocks, "thinking", ev.data.text, !!ev.data.redacted),
      }));

    case "text_delta":
      return updateMessage(s, ev.message_id, (m) => ({ ...firstToken(m, ev.ts), blocks: appendText(m.blocks, "text", ev.data.text, false) }));

    case "image":
      return updateMessage(s, ev.message_id, (m) => ({
        ...firstToken(m, ev.ts),
        blocks: [...m.blocks, { type: "image", mediaType: ev.data.media_type, data: ev.data.data, url: ev.data.url }],
      }));

    case "tool_call": {
      const d = ev.data;
      const tool: ToolView = { callId: d.call_id, name: d.name, input: d.input, risk: d.risk, status: "queued" };
      const next = { ...s, tools: { ...s.tools, [d.call_id]: tool } };
      return updateMessage(next, ev.message_id, (m) => ({ ...firstToken(m, ev.ts), blocks: [...m.blocks, { type: "tool", callId: d.call_id }] }));
    }

    case "message_end": {
      // 每次模型调用结束就把用量累加进会话用量，状态栏的 token 数随之实时变化；
      // turn_end 会再用后端的权威值（session_usage）覆盖，所以这里的累加不会累积误差。
      const live = s.running ? addUsage(s.sessionUsage, ev.data.usage) : s.sessionUsage;
      return updateMessage({ ...s, sessionUsage: live }, ev.message_id, (m) => ({
        ...m,
        streaming: false,
        stopReason: ev.data.stop_reason,
        usage: ev.data.usage,
      }));
    }

    case "permission_request": {
      const d = ev.data;
      const next = updateTool(s, d.call_id, (t) => ({
        ...t,
        status: "awaiting_permission",
        permission: { requestId: d.request_id, summary: d.summary },
      }));
      return { ...next, permissions: [...next.permissions, d] };
    }

    case "permission_resolved": {
      const d = ev.data;
      const next = updateTool(s, d.call_id, (t) => ({
        ...t,
        status: d.decision === "deny" ? "denied" : "running",
        permission: t.permission ? { ...t.permission, decision: d.decision } : undefined,
      }));
      return { ...next, permissions: next.permissions.filter((p) => p.request_id !== d.request_id) };
    }

    case "tool_result": {
      const d = ev.data;
      // 重放历史时没有 tool_call 之前的权限事件，工具可能还没登记，补一个最小记录。
      const base = s.tools[d.call_id] ?? { callId: d.call_id, name: d.name, input: {}, risk: "" as Risk, status: "queued" as ToolStatus };
      const status: ToolStatus = base.status === "denied" ? "denied" : d.is_error ? "error" : "done";
      return {
        ...s,
        tools: { ...s.tools, [d.call_id]: { ...base, status, output: d.output, durationMs: d.duration_ms } },
      };
    }

    case "turn_end": {
      const d = ev.data;
      const turnMs = !d.replay && s.turnStartedAt !== null ? Date.parse(ev.ts) - s.turnStartedAt : null;
      let lastAssistant = -1;
      s.timeline.forEach((it, i) => it.kind === "assistant" && (lastAssistant = i));
      return {
        ...s,
        running: false,
        permissions: [],
        sessionUsage: d.session_usage,
        turnStartedAt: null,
        lastTurnMs: turnMs ?? s.lastTurnMs,
        // 本轮所有还在流式中的消息都结束了（例如被取消时可能没有 message_end）；总耗时记在本轮最后一条消息上。
        timeline: [
          ...s.timeline.map((it, i) => {
            if (it.kind !== "assistant") return it;
            const withMs = i === lastAssistant && turnMs !== null;
            return it.streaming || withMs ? { ...it, streaming: false, ...(withMs ? { turnMs } : {}) } : it;
          }),
          { kind: "turn_end", id: ev.id, reason: d.reason, steps: d.steps, usage: d.usage, replay: !!d.replay },
        ],
      };
    }

    case "error":
      return {
        ...s,
        timeline: [...s.timeline, { kind: "error", id: ev.id, code: ev.data.code, message: ev.data.message, fatal: ev.data.fatal }],
      };

    // 标题不在 reducer 的状态里（它属于会话元数据，由 chatStore 处理）。
    case "session_updated":
      return s;

    case "context_compacted":
      return {
        ...s,
        timeline: [
          ...s.timeline,
          { kind: "notice", id: ev.id, text: `上下文已压缩：${ev.data.before_tokens} → ${ev.data.after_tokens} tokens，移除 ${ev.data.removed_messages} 条消息` },
        ],
      };

    // ---- 预留能力：子 agent 与后台任务 ----
    case "subagent_start": {
      const d = ev.data;
      return { ...s, agents: { ...s.agents, [d.agent_id]: { id: d.agent_id, parentId: d.parent_id, name: d.name, task: d.task, status: "running" } } };
    }
    case "subagent_end": {
      const a = s.agents[ev.data.agent_id];
      if (!a) return s;
      return { ...s, agents: { ...s.agents, [a.id]: { ...a, status: ev.data.status } } };
    }
    case "task_start":
      return { ...s, tasks: { ...s.tasks, [ev.data.task_id]: { id: ev.data.task_id, title: ev.data.title, status: "running", log: "" } } };
    case "task_log": {
      const t = s.tasks[ev.data.task_id];
      if (!t) return s;
      return { ...s, tasks: { ...s.tasks, [t.id]: { ...t, log: (t.log + ev.data.text).slice(-20000) } } };
    }
    case "task_end": {
      const t = s.tasks[ev.data.task_id];
      if (!t) return s;
      return { ...s, tasks: { ...s.tasks, [t.id]: { ...t, status: ev.data.status, exitCode: ev.data.exit_code } } };
    }
  }
  return s;
}

function addUsage(a: Usage, b: Usage): Usage {
  return {
    input_tokens: a.input_tokens + b.input_tokens,
    output_tokens: a.output_tokens + b.output_tokens,
    cache_read_tokens: (a.cache_read_tokens ?? 0) + (b.cache_read_tokens ?? 0),
    cache_write_tokens: (a.cache_write_tokens ?? 0) + (b.cache_write_tokens ?? 0),
  };
}

/** 追加文本增量：与最后一个同类块合并，否则新开一个块。 */
function appendText(blocks: AssistantBlock[], type: "text" | "thinking", text: string, redacted: boolean): AssistantBlock[] {
  const last = blocks[blocks.length - 1];
  if (last && last.type === type && !(last.type === "thinking" && (last.redacted || redacted))) {
    return [...blocks.slice(0, -1), { ...last, text: last.text + text }];
  }
  return [...blocks, type === "thinking" ? { type, text, redacted } : { type, text }];
}

/** 记录第一个内容到达的时间（只在实时的调用上记录，重放的历史没有 startedAt）。时间取事件信封的 ts（后端时间）。 */
function firstToken(m: AssistantItem, ts: string): AssistantItem {
  if (m.startedAt === undefined || m.firstTokenAt !== undefined) return m;
  return { ...m, firstTokenAt: Date.parse(ts) };
}

function updateMessage(s: ChatState, messageId: string | undefined, fn: (m: AssistantItem) => AssistantItem): ChatState {
  if (!messageId) return s;
  // 从后往前找：正在更新的几乎总是最后一条消息。
  for (let i = s.timeline.length - 1; i >= 0; i--) {
    const it = s.timeline[i];
    if (it.kind === "assistant" && it.id === messageId) {
      const timeline = s.timeline.slice();
      timeline[i] = fn(it);
      return { ...s, timeline };
    }
  }
  return s;
}

function updateTool(s: ChatState, callId: string, fn: (t: ToolView) => ToolView): ChatState {
  const t = s.tools[callId];
  if (!t) return s;
  return { ...s, tools: { ...s.tools, [callId]: fn(t) } };
}
