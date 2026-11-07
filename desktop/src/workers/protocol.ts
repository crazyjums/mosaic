// 壳的主进程 ⇄ 引擎 worker（utilityProcess）之间的消息。两边只靠这些消息通信，worker 里没有别的出口。

/** 一轮的输入（Go 端 remote.Spec）。 */
export interface RunSpec {
  engine: string;
  session_id: string;
  /** 厂商配置档的名字，消息头显示它。 */
  profile?: string;
  text: string;
  workspace: string;
  model?: string;
  permission_mode: string;
  options?: Record<string, unknown>;
  resume?: string;
}

/** 一轮的结果（Go 端 remote.Outcome）。messages 是 provider.Message 的 JSON（蛇形字段名）。 */
export interface Outcome {
  reason: "completed" | "cancelled" | "error";
  error?: string;
  resume?: string;
  /** 找不到恢复凭据指向的会话，什么都没做：Go 端据此把完整的历史补给引擎再重跑（不要在发过事件之后用）。 */
  resume_lost?: boolean;
  usage: { input_tokens: number; output_tokens: number; cache_read_tokens?: number; cache_write_tokens?: number };
  messages?: unknown[];
}

/** 引擎要用户确认的操作（Go 端 remote.AskRequest）。 */
export interface AskRequest {
  call_id: string;
  tool: string;
  risk: "read" | "write" | "exec";
  sensitive?: boolean;
  summary: string;
  input?: unknown;
}

export type ToWorker = { type: "run"; spec: RunSpec } | { type: "cancel" } | { type: "answer"; id: number; decision: "allow" | "allow_session" | "deny" };

export type FromWorker =
  | { type: "emit"; message_id: string; event_type: string; data: unknown }
  | { type: "ask"; id: number; request: AskRequest }
  | { type: "done"; outcome: Outcome };

/** worker 里用的收发接口（utilityProcess 的 parentPort，或测试里的 IPC 通道）。 */
export interface WorkerIO {
  post(msg: FromWorker): void;
  onMessage(cb: (msg: ToWorker) => void): void;
}
