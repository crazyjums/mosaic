// 统一事件协议的 TypeScript 镜像。
//
// 权威定义在 Go 端 internal/event（payloads.go），协议说明见 docs/event-protocol.md。
// 两边必须同步修改；前端遇到不认识的事件类型时应忽略，而不是报错（向前兼容）。
//
// 这里用"判别联合"（discriminated union）：先看 `type`，TypeScript 就能自动推断出 `data` 的结构。

export const PROTOCOL_VERSION = 1;

export const ROOT_AGENT_ID = "main";

export interface Usage {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export type TurnEndReason = "completed" | "max_steps" | "cancelled" | "error";
export type Risk = "read" | "write" | "exec" | "";
export type Decision = "allow" | "allow_session" | "deny";

/** 每种事件类型对应的载荷。键就是事件的 type 字段。 */
export interface PayloadMap {
  turn_start: { turn_id: string; provider: string; model: string; replay?: boolean };
  turn_end: {
    turn_id: string;
    reason: TurnEndReason;
    steps: number;
    usage: Usage;
    session_usage: Usage;
    replay?: boolean;
  };
  user_message: { text: string };

  message_start: { provider: string; model: string; step: number };
  text_delta: { text: string };
  thinking_delta: { text: string; redacted?: boolean };
  image: { media_type: string; data?: string; url?: string; alt?: string };
  message_end: { stop_reason: string; usage: Usage };

  tool_call: { call_id: string; name: string; input: unknown; risk: Risk };
  tool_result: { call_id: string; name: string; output: string; is_error: boolean; duration_ms: number };
  permission_request: {
    request_id: string;
    call_id: string;
    tool: string;
    risk: Risk;
    summary: string;
    input: unknown;
  };
  permission_resolved: { request_id: string; call_id: string; decision: Decision; reason?: string };

  // 预留能力：第一阶段后端不会产生，但前端已能接收和展示。
  subagent_start: { agent_id: string; parent_id: string; name: string; task: string };
  subagent_end: { agent_id: string; parent_id: string; status: string; summary?: string };
  task_start: { task_id: string; title: string; kind: string };
  task_log: { task_id: string; stream: string; text: string };
  task_end: { task_id: string; status: string; exit_code: number };
  context_compacted: { before_tokens: number; after_tokens: number; removed_messages: number; summary?: string };

  /** 使用模型组时，说明这条消息实际由哪个成员应答，以及前面有哪些尝试被转交 */
  route: {
    group: string;
    profile: string;
    model: string;
    try: number;
    handed_on?: { profile: string; model: string; kind: string; message: string }[];
  };

  error: { code: string; message: string; fatal: boolean };

  /** 会话元数据变了（目前是由模型生成的标题，在第一轮结束之后到达） */
  session_updated: { title: string };
}

export type EventType = keyof PayloadMap;

/** 事件信封。字段与 Go 的 event.Event 一致。 */
export interface Envelope<T extends EventType = EventType> {
  v: number;
  type: T;
  id: string;
  seq: number;
  session_id: string;
  agent_id: string;
  parent_id?: string;
  message_id?: string;
  ts: string;
  data: PayloadMap[T];
}

/** 所有已知事件的联合类型：`switch (ev.type)` 之后 `ev.data` 会被自动收窄。 */
export type AgentEvent = { [K in EventType]: Envelope<K> }[EventType];

const KNOWN_TYPES = new Set<string>([
  "turn_start", "turn_end", "user_message",
  "message_start", "text_delta", "thinking_delta", "image", "message_end", "route",
  "tool_call", "tool_result", "permission_request", "permission_resolved",
  "subagent_start", "subagent_end", "task_start", "task_log", "task_end",
  "context_compacted", "error", "session_updated",
]);

/** 判断一个来自后端的对象是否是本前端认识的事件。 */
export function isKnownEvent(x: unknown): x is AgentEvent {
  return typeof x === "object" && x !== null && KNOWN_TYPES.has((x as { type?: string }).type ?? "");
}
