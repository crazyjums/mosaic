import type { Outcome } from "./protocol";
import { riskOf as ccRisk } from "./cc-translate";
import type { Sink } from "./cc-translate";

// 把 pi（@earendil-works/pi-coding-agent）的会话事件翻译成 Mosaic 的统一事件，并累积出要存进会话历史的消息。
//
// 事件形状来自对真实 SDK（1.5.0）的探测（假的 OpenAI 兼容服务器 + 真 pi），不是凭文档猜的：
//   message_start / message_end   → 每条消息（system、user、assistant、toolResult 各一对）；message_end 带完整消息
//   message_update                → assistantMessageEvent：text_delta / thinking_delta / toolcall_end（带完整工具调用）…
//   tool_execution_start / _end   → 工具执行（_end 带结果和 isError）
//   turn_end / agent_end          → 一次模型调用 / 整个运行结束
//   agent_settled                 → pi 不会再自动继续了（本轮真正结束）
// system 和 user 消息不转发：用户消息由内核自己发，system 是 pi 的提示词。

export interface PiEvent {
  type?: string;
  message?: PiMessage;
  assistantMessageEvent?: { type?: string; delta?: string; content?: string; toolCall?: { id?: string; name?: string; arguments?: unknown } };
  toolCallId?: string;
  toolName?: string;
  args?: unknown;
  result?: { content?: unknown };
  isError?: boolean;
  durationMs?: number;
  aborted?: boolean;
}

interface PiMessage {
  role?: string;
  content?: unknown;
  provider?: string;
  model?: string;
  stopReason?: string;
  errorMessage?: string;
  usage?: { input?: number; output?: number; cacheRead?: number; cacheWrite?: number };
  toolCallId?: string;
  toolName?: string;
  isError?: boolean;
}

type Block =
  | { type: "text"; text: string }
  | { type: "thinking"; text: string }
  | { type: "tool_use"; tool_call_id: string; tool_name: string; tool_input: unknown }
  | { type: "tool_result"; tool_call_id: string; text: string; is_error: boolean };

interface Msg {
  role: "user" | "assistant";
  content: Block[];
}

/** pi 内置工具的风险级别；不认识的（扩展注册的）按最高（exec）处理。 */
export function piRisk(tool: string): "read" | "write" | "exec" {
  switch (tool) {
    case "read":
    case "grep":
    case "find":
    case "ls":
      return "read";
    case "write":
    case "edit":
      return "write";
    default:
      return ccRisk(tool) === "read" ? "read" : "exec";
  }
}

const STOP: Record<string, string> = { stop: "end_turn", toolUse: "tool_use", length: "max_tokens", aborted: "cancelled", error: "error" };

export class PiTranslator {
  private usage = { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 };
  private step = 0;
  private seq = 0;
  private openMsg = ""; // 当前打开的助手消息的 Mosaic 消息 ID
  private history: Msg[] = [];
  private toolNames = new Map<string, string>();
  private resulted = new Set<string>();
  /** 模型报告的错误（stopReason 为 error 时的 errorMessage）：pi 不抛异常，错误在消息里。 */
  lastError = "";

  constructor(
    private readonly sink: Sink,
    private readonly engine = "pi",
    private readonly model = "",
  ) {}

  private newMsgId(): string {
    return `msg_${Date.now().toString(36)}_${(this.seq++).toString(36)}`;
  }

  handle(ev: PiEvent): void {
    switch (ev.type) {
      case "message_start":
        if (ev.message?.role === "assistant") {
          this.closeOpen("end_turn");
          this.step++;
          this.openMsg = this.newMsgId();
          this.sink.emit(this.openMsg, "message_start", { provider: this.engine, model: ev.message.model || this.model, step: this.step });
        }
        return;
      case "message_update":
        return this.onUpdate(ev);
      case "message_end":
        return this.onMessageEnd(ev.message);
      case "tool_execution_end":
        return this.onToolEnd(ev);
    }
  }

  private onUpdate(ev: PiEvent): void {
    const a = ev.assistantMessageEvent;
    if (!a || !this.openMsg) return;
    switch (a.type) {
      case "text_delta":
        if (a.delta) this.sink.emit(this.openMsg, "text_delta", { text: a.delta });
        return;
      case "thinking_delta":
        if (a.delta) this.sink.emit(this.openMsg, "thinking_delta", { text: a.delta });
        return;
      case "toolcall_end": {
        const c = a.toolCall;
        if (!c?.id) return;
        const name = c.name ?? "";
        this.toolNames.set(c.id, name);
        this.sink.emit(this.openMsg, "tool_call", { call_id: c.id, name, input: c.arguments ?? {}, risk: piRisk(name) });
        return;
      }
    }
  }

  private closeOpen(stop: string, m?: PiMessage): void {
    if (!this.openMsg) return;
    const u = m?.usage ?? {};
    this.sink.emit(this.openMsg, "message_end", {
      stop_reason: stop,
      usage: { input_tokens: u.input ?? 0, output_tokens: u.output ?? 0, cache_read_tokens: u.cacheRead ?? 0, cache_write_tokens: u.cacheWrite ?? 0 },
    });
    this.openMsg = "";
  }

  private onMessageEnd(m?: PiMessage): void {
    if (!m) return;
    if (m.role === "assistant") {
      const u = m.usage ?? {};
      this.usage.input_tokens += u.input ?? 0;
      this.usage.output_tokens += u.output ?? 0;
      this.usage.cache_read_tokens += u.cacheRead ?? 0;
      this.usage.cache_write_tokens += u.cacheWrite ?? 0;
      if (m.stopReason === "error" && m.errorMessage) this.lastError = m.errorMessage;
      const blocks: Block[] = [];
      for (const b of Array.isArray(m.content) ? (m.content as Record<string, unknown>[]) : []) {
        if (b.type === "text" && b.text) blocks.push({ type: "text", text: String(b.text) });
        else if (b.type === "thinking" && b.thinking) blocks.push({ type: "thinking", text: String(b.thinking) });
        else if (b.type === "toolCall") blocks.push({ type: "tool_use", tool_call_id: String(b.id ?? ""), tool_name: String(b.name ?? ""), tool_input: b.arguments ?? {} });
      }
      if (blocks.length > 0) this.history.push({ role: "assistant", content: blocks });
      this.closeOpen(STOP[m.stopReason ?? ""] ?? "end_turn", m);
    }
    // toolResult 的内容在 tool_execution_end 里处理（那里最先到）；这里只需要保证历史里有它。
  }

  private onToolEnd(ev: PiEvent): void {
    const id = ev.toolCallId ?? "";
    if (!id || this.resulted.has(id)) return;
    this.resulted.add(id);
    const name = ev.toolName ?? this.toolNames.get(id) ?? "";
    const text = resultText(ev.result?.content);
    const isError = ev.isError === true;
    this.sink.emit("", "tool_result", { call_id: id, name, output: text, is_error: isError, duration_ms: ev.durationMs ?? 0 });
    // 同一批工具结果合成一条 user 消息，和 provider.Message 的约定一致。
    const last = this.history[this.history.length - 1];
    const block: Block = { type: "tool_result", tool_call_id: id, text, is_error: isError };
    if (last && last.role === "user" && last.content.every((b) => b.type === "tool_result")) last.content.push(block);
    else this.history.push({ role: "user", content: [block] });
  }

  /** 收尾：关掉还开着的消息，补齐没有结果的工具调用，返回本轮的结果。resume 是 pi 会话文件的路径。 */
  finish(reason: Outcome["reason"], error = "", resume = ""): Outcome {
    this.closeOpen(reason === "cancelled" ? "cancelled" : reason === "error" ? "error" : "end_turn");
    // 每个 tool_use 都要有 tool_result，否则这段历史没法再发给任何模型。
    const missing: Block[] = [];
    for (const msg of this.history) {
      for (const b of msg.content) {
        if (b.type === "tool_use" && !this.resulted.has(b.tool_call_id)) {
          this.resulted.add(b.tool_call_id);
          missing.push({ type: "tool_result", tool_call_id: b.tool_call_id, text: "（没有拿到结果：这一轮在它完成前结束了）", is_error: true });
          this.sink.emit("", "tool_result", { call_id: b.tool_call_id, name: b.tool_name, output: "（没有拿到结果：这一轮在它完成前结束了）", is_error: true, duration_ms: 0 });
        }
      }
    }
    if (missing.length > 0) this.history.push({ role: "user", content: missing });
    return { reason, error: error || undefined, resume: resume || undefined, usage: this.usage, messages: this.history };
  }
}

function resultText(c: unknown): string {
  if (typeof c === "string") return c;
  if (!Array.isArray(c)) return c == null ? "" : JSON.stringify(c);
  return c
    .map((b) => {
      const x = b as { type?: string; text?: string };
      return x.type === "text" ? (x.text ?? "") : x.type === "image" ? "[图片]" : "";
    })
    .filter(Boolean)
    .join("\n");
}
