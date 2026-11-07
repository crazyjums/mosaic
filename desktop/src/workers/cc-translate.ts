import type { Outcome } from "./protocol";

// 把 Claude Agent SDK 的消息流翻译成 Mosaic 的统一事件，并累积出要存进会话历史的消息。
//
// 消息形状来自对真实 SDK（0.3.319）加真实 claude 的探测，不是凭文档猜的：
//   system/init            → 会话 ID（恢复用）
//   stream_event           → 流式增量：message_start、content_block_start/delta/stop、message_delta、message_stop
//   assistant              → 一个内容块完成（同一条 API 消息的各块分多次到达，message.id 相同）
//   user（tool_result 块）  → 工具执行完的结果
//   result                 → 本轮结束：subtype、usage、session_id、错误
// 子 agent 里发生的事（parent_tool_use_id 非空）不显示：界面上只有主 agent 的过程。

export interface Sink {
  emit(messageId: string, type: string, data: unknown): void;
}

/** SDK 消息：只声明用到的字段，其余忽略（SDK 升级加字段不影响我们）。 */
export interface SdkMessage {
  type?: string;
  subtype?: string;
  session_id?: string;
  parent_tool_use_id?: string | null;
  event?: {
    type?: string;
    message?: { id?: string; model?: string };
    index?: number;
    content_block?: { type?: string; id?: string; name?: string };
    delta?: { type?: string; text?: string; thinking?: string; stop_reason?: string | null };
    usage?: Record<string, number>;
  };
  message?: { id?: string; model?: string; content?: unknown };
  // result
  is_error?: boolean;
  result?: string;
  errors?: string[];
  usage?: Record<string, number>;
}

type Block =
  | { type: "text"; text: string }
  | { type: "thinking"; text: string; signature?: string }
  | { type: "tool_use"; tool_call_id: string; tool_name: string; tool_input: unknown }
  | { type: "tool_result"; tool_call_id: string; text: string; is_error: boolean };

interface Msg {
  role: "user" | "assistant";
  content: Block[];
}

const READ_TOOLS = new Set(["Read", "Glob", "Grep", "LS", "WebFetch", "WebSearch", "TodoWrite", "ToolSearch", "NotebookRead"]);
const WRITE_TOOLS = new Set(["Write", "Edit", "MultiEdit", "NotebookEdit"]);

/** 工具的风险级别：界面据此决定确认弹窗的样子。不认识的按最高（exec）处理。 */
export function riskOf(tool: string): "read" | "write" | "exec" {
  if (READ_TOOLS.has(tool)) return "read";
  if (WRITE_TOOLS.has(tool)) return "write";
  return "exec";
}

export class ClaudeTranslator {
  sessionId = "";
  /** 本轮结束的结果（收到 result 消息后有值）。 */
  result?: { ok: boolean; error: string };
  private usage = { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 };

  private step = 0;
  /** API 消息 ID → Mosaic 消息 ID：同一条助手消息的所有事件共用一个 ID。 */
  private msgIds = new Map<string, string>();
  private openMsg = ""; // 当前打开的（还没发 message_end 的）Mosaic 消息 ID
  private streamed = new Set<string>(); // 已经通过流式事件发过内容的 API 消息 ID
  private toolNames = new Map<string, string>();
  private emittedCalls = new Set<string>();

  private history: Msg[] = [];
  private pending?: { apiId: string; blocks: Block[] };
  private seq = 0;

  constructor(
    private readonly sink: Sink,
    private readonly engine = "claudecode",
    private readonly model = "",
  ) {}

  private newMsgId(): string {
    return `msg_${Date.now().toString(36)}_${(this.seq++).toString(36)}`;
  }

  handle(m: SdkMessage): void {
    if (m.parent_tool_use_id) return; // 子 agent 的过程不显示
    switch (m.type) {
      case "system":
        if (m.subtype === "init" && m.session_id) this.sessionId = m.session_id;
        return;
      case "stream_event":
        return this.onStream(m);
      case "assistant":
        return this.onAssistant(m);
      case "user":
        return this.onUser(m);
      case "result":
        return this.onResult(m);
    }
  }

  // ---- 流式增量 ----

  private onStream(m: SdkMessage): void {
    const e = m.event;
    if (!e) return;
    switch (e.type) {
      case "message_start": {
        this.closeOpen("end_turn");
        const apiId = e.message?.id ?? this.newMsgId();
        const id = this.newMsgId();
        this.msgIds.set(apiId, id);
        this.streamed.add(apiId);
        this.openMsg = id;
        this.step++;
        this.sink.emit(id, "message_start", { provider: this.engine, model: e.message?.model || this.model, step: this.step });
        return;
      }
      case "content_block_delta": {
        if (!this.openMsg) return;
        const d = e.delta;
        if (d?.type === "text_delta" && d.text) this.sink.emit(this.openMsg, "text_delta", { text: d.text });
        else if (d?.type === "thinking_delta" && d.thinking) this.sink.emit(this.openMsg, "thinking_delta", { text: d.thinking });
        return;
      }
      case "message_delta":
        this.stopReason = e.delta?.stop_reason ?? this.stopReason;
        if (e.usage) this.msgUsage = e.usage;
        return;
      case "message_stop":
        this.closeOpen(this.stopReason || "end_turn");
        return;
    }
  }

  private stopReason = "";
  private msgUsage: Record<string, number> = {};

  private closeOpen(stop: string): void {
    if (!this.openMsg) return;
    const u = this.msgUsage;
    this.sink.emit(this.openMsg, "message_end", {
      stop_reason: stop,
      usage: { input_tokens: u.input_tokens ?? 0, output_tokens: u.output_tokens ?? 0, cache_read_tokens: u.cache_read_input_tokens ?? 0, cache_write_tokens: u.cache_creation_input_tokens ?? 0 },
    });
    this.openMsg = "";
    this.stopReason = "";
    this.msgUsage = {};
  }

  // ---- 完整的块 ----

  private onAssistant(m: SdkMessage): void {
    const apiId = m.message?.id ?? this.newMsgId();
    const content = Array.isArray(m.message?.content) ? (m.message!.content as Record<string, unknown>[]) : [];
    // 没有流式事件（没开 partial）时，这里补全 message_start 和文字，保证界面照样能显示。
    let id = this.msgIds.get(apiId);
    const synthesized = !id;
    if (!id) {
      this.closeOpen("end_turn");
      id = this.newMsgId();
      this.msgIds.set(apiId, id);
      this.openMsg = id;
      this.step++;
      this.sink.emit(id, "message_start", { provider: this.engine, model: m.message?.model || this.model, step: this.step });
    }
    const blocks: Block[] = [];
    for (const b of content) {
      switch (b.type) {
        case "text":
          blocks.push({ type: "text", text: String(b.text ?? "") });
          if (synthesized && b.text) this.sink.emit(id, "text_delta", { text: String(b.text) });
          break;
        case "thinking":
          blocks.push({ type: "thinking", text: String(b.thinking ?? ""), signature: typeof b.signature === "string" ? b.signature : undefined });
          if (synthesized && b.thinking) this.sink.emit(id, "thinking_delta", { text: String(b.thinking) });
          break;
        case "tool_use": {
          const callId = String(b.id);
          const name = String(b.name ?? "");
          blocks.push({ type: "tool_use", tool_call_id: callId, tool_name: name, tool_input: b.input ?? {} });
          this.toolNames.set(callId, name);
          if (!this.emittedCalls.has(callId)) {
            this.emittedCalls.add(callId);
            this.sink.emit(id, "tool_call", { call_id: callId, name, input: b.input ?? {}, risk: riskOf(name) });
          }
          break;
        }
      }
    }
    this.addToPending(apiId, blocks);
    if (synthesized) this.closeOpen(blocks.some((b) => b.type === "tool_use") ? "tool_use" : "end_turn");
  }

  private addToPending(apiId: string, blocks: Block[]): void {
    if (this.pending && this.pending.apiId !== apiId) this.flushAssistant();
    if (!this.pending) this.pending = { apiId, blocks: [] };
    for (const b of blocks) {
      const dup =
        (b.type === "tool_use" && this.pending.blocks.some((x) => x.type === "tool_use" && x.tool_call_id === b.tool_call_id)) ||
        (b.type !== "tool_use" && this.pending.blocks.some((x) => x.type === b.type && "text" in x && "text" in b && x.text === b.text));
      if (!dup) this.pending.blocks.push(b);
    }
  }

  private flushAssistant(): void {
    if (!this.pending) return;
    const blocks = this.pending.blocks.filter((b) => !(b.type === "text" && b.text === ""));
    this.pending = undefined;
    if (blocks.length === 0) return;
    const last = this.history[this.history.length - 1];
    if (last && last.role === "assistant") last.content.push(...blocks); // 相邻的助手消息并成一条：历史要求角色交替
    else this.history.push({ role: "assistant", content: blocks });
  }

  // ---- 工具结果 ----

  private onUser(m: SdkMessage): void {
    const content = Array.isArray(m.message?.content) ? (m.message!.content as Record<string, unknown>[]) : [];
    const results = content.filter((b) => b.type === "tool_result");
    if (results.length === 0) return;
    this.closeOpen("tool_use");
    this.flushAssistant();
    const blocks: Block[] = [];
    for (const r of results) {
      const callId = String(r.tool_use_id);
      const text = resultText(r.content);
      const isError = r.is_error === true;
      this.sink.emit("", "tool_result", { call_id: callId, name: this.toolNames.get(callId) ?? "", output: text, is_error: isError, duration_ms: 0 });
      blocks.push({ type: "tool_result", tool_call_id: callId, text, is_error: isError });
    }
    const last = this.history[this.history.length - 1];
    if (last && last.role === "user") last.content.push(...blocks);
    else this.history.push({ role: "user", content: blocks });
  }

  // ---- 结束 ----

  private onResult(m: SdkMessage): void {
    if (m.session_id) this.sessionId = m.session_id;
    const u = m.usage ?? {};
    this.usage = {
      input_tokens: u.input_tokens ?? 0,
      output_tokens: u.output_tokens ?? 0,
      cache_read_tokens: u.cache_read_input_tokens ?? 0,
      cache_write_tokens: u.cache_creation_input_tokens ?? 0,
    };
    const ok = m.subtype === "success" && m.is_error !== true;
    this.result = { ok, error: ok ? "" : (m.errors?.join("；") || m.result || `Claude Code 报告失败（${m.subtype ?? "unknown"}）`) };
  }

  /** 收尾：关掉还开着的消息，补齐没有结果的工具调用，返回本轮的结果。 */
  finish(reason: Outcome["reason"], error = ""): Outcome {
    this.closeOpen(reason === "cancelled" ? "cancelled" : reason === "error" ? "error" : "end_turn");
    this.flushAssistant();
    // 每个 tool_use 都要有 tool_result，否则这段历史没法再发给任何模型。
    const have = new Set<string>();
    for (const msg of this.history) for (const b of msg.content) if (b.type === "tool_result") have.add(b.tool_call_id);
    const missing: Block[] = [];
    for (const msg of this.history) {
      for (const b of msg.content) {
        if (b.type === "tool_use" && !have.has(b.tool_call_id)) {
          missing.push({ type: "tool_result", tool_call_id: b.tool_call_id, text: "（没有拿到结果：这一轮在它完成前结束了）", is_error: true });
          this.sink.emit("", "tool_result", { call_id: b.tool_call_id, name: b.tool_name, output: "（没有拿到结果：这一轮在它完成前结束了）", is_error: true, duration_ms: 0 });
        }
      }
    }
    if (missing.length > 0) this.history.push({ role: "user", content: missing });
    return {
      reason,
      error: error || undefined,
      resume: this.sessionId || undefined,
      usage: this.usage,
      messages: this.history,
    };
  }
}

/** tool_result 的 content 可能是字符串，也可能是内容块数组（文本、图片）。 */
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
