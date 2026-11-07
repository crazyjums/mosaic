import { existsSync } from "node:fs";
import { PiTranslator, piRisk, type PiEvent } from "./pi-translate";
import type { AskRequest, Outcome, RunSpec, ToWorker, WorkerIO } from "./protocol";
import { serveWorker } from "./serve";

// pi 引擎（pi 编程 agent 的 SDK：@earendil-works/pi-coding-agent）。跑在 Electron 的 utilityProcess 里，
// 和主进程只靠 protocol.ts 的消息通信。
//
// 模型和凭证都是用户自己的 pi 配置（~/.pi/agent：settings.json、auth.json、models.json）：Mosaic 不读也不保存它们。
// pi 自己没有“每次调用前确认”的机制，这里用扩展的 tool_call 钩子把它接到 Mosaic 的权限确认上。

/** 只声明用到的 SDK 部分；其余忽略（SDK 升级加字段不影响我们）。 */
export interface PiSession {
  sessionFile?: string;
  sessionId: string;
  subscribe(cb: (ev: PiEvent) => void): () => void;
  prompt(text: string): Promise<void>;
  abort(): Promise<void>;
  dispose(): void;
}
export interface PiExtensionApi {
  on(event: "tool_call", handler: (e: { toolName: string; toolCallId: string; input: Record<string, unknown> }) => Promise<{ block: boolean; reason?: string } | undefined>): void;
}
export interface PiModelRuntime {
  getModel(provider: string, id: string): unknown;
  getAvailable(): Promise<{ provider: string; id: string }[]>;
}
export interface PiSdk {
  ModelRuntime: { create(opts?: Record<string, unknown>): Promise<PiModelRuntime> };
  SessionManager: { create(cwd: string, dir?: string): unknown; open(path: string): unknown };
  DefaultResourceLoader: new (opts: Record<string, unknown>) => { reload(): Promise<void> };
  getAgentDir(): string;
  createAgentSession(opts: Record<string, unknown>): Promise<{ session: PiSession; modelFallbackMessage?: string }>;
}
export type PiSdkLoader = () => Promise<PiSdk>;

const READ_ONLY_TOOLS = ["read", "grep", "find", "ls"];
const NO_MODEL = "pi 没有可用的模型。请先在终端运行 pi 并用 /login 登录，或在 ~/.pi/agent 里配置 API Key，再回来重试";

type Decision = "allow" | "allow_session" | "deny";

/** 这个权限模式下，这个风险级别的工具调用要不要先问用户。 */
export function needsAsk(mode: string, risk: "read" | "write" | "exec"): boolean {
  switch (mode) {
    case "auto":
      return false;
    case "accept_edits":
      return risk === "exec";
    default: // default 和 readonly（readonly 下写/执行工具根本没开，这里是第二道防线）
      return risk !== "read";
  }
}

function summarize(tool: string, input: Record<string, unknown>): string {
  const pick = (k: string) => (typeof input[k] === "string" ? (input[k] as string) : "");
  const detail = pick("command") || pick("path") || pick("file_path") || pick("pattern");
  const text = detail ? `${tool}：${detail}` : tool;
  return text.length > 200 ? text.slice(0, 200) + "…" : text;
}

/** 运行一轮。io 是和主进程之间的通道；loadSdk 在测试里可以换成假的。 */
export async function runPi(spec: RunSpec, io: WorkerIO, loadSdk: PiSdkLoader): Promise<Outcome> {
  const tr = new PiTranslator({ emit: (message_id, event_type, data) => io.post({ type: "emit", message_id, event_type, data }) }, spec.profile || "pi", spec.model);
  const fail = (error: string, resume = "") => tr.finish("error", error, resume);

  let cancelled = false;
  let session: PiSession | undefined;
  const pendingAsks = new Map<number, (d: Decision) => void>();
  let askSeq = 0;
  io.onMessage((m: ToWorker) => {
    if (m.type === "cancel") {
      cancelled = true;
      for (const resolve of pendingAsks.values()) resolve("deny"); // 等待中的确认按拒绝处理
      pendingAsks.clear();
      void session?.abort().catch(() => undefined);
    } else if (m.type === "answer") {
      pendingAsks.get(m.id)?.(m.decision);
      pendingAsks.delete(m.id);
    }
  });
  const ask = (request: AskRequest) =>
    new Promise<Decision>((resolve) => {
      const id = ++askSeq;
      pendingAsks.set(id, resolve);
      io.post({ type: "ask", id, request });
    });

  let sdk: PiSdk;
  try {
    sdk = await loadSdk();
  } catch (e) {
    return fail(`无法加载 pi SDK：${e instanceof Error ? e.message : String(e)}`);
  }
  if (cancelled) return tr.finish("cancelled");

  try {
    const modelRuntime = await sdk.ModelRuntime.create();
    // 模型写作 "提供商/模型ID"（pi 的写法）；留空 / default 用 pi 自己配置的默认模型。
    let model: unknown;
    if (spec.model && spec.model !== "default") {
      const slash = spec.model.indexOf("/");
      if (slash <= 0) return fail(`模型要写成 提供商/模型ID（例如 anthropic/claude-sonnet-4-5），收到 ${spec.model}`);
      model = modelRuntime.getModel(spec.model.slice(0, slash), spec.model.slice(slash + 1));
      if (!model) return fail(`pi 里没有模型 ${spec.model}。可用的模型见终端里 pi --list-models`);
    }

    const sessionAllowed = new Set<string>();
    const mode = spec.permission_mode;
    const agentDir = sdk.getAgentDir();
    const loader = new sdk.DefaultResourceLoader({
      cwd: spec.workspace,
      agentDir,
      extensionFactories: [
        (pi: PiExtensionApi) => {
          pi.on("tool_call", async (e) => {
            const risk = piRisk(e.toolName);
            if (!needsAsk(mode, risk) || sessionAllowed.has(e.toolName)) return undefined;
            const decision = await ask({ call_id: e.toolCallId, tool: e.toolName, risk, summary: summarize(e.toolName, e.input), input: e.input });
            if (decision === "deny") return { block: true, reason: cancelled ? "用户停止了这一轮" : "用户拒绝了这次操作" };
            if (decision === "allow_session") sessionAllowed.add(e.toolName);
            return undefined;
          });
        },
      ],
    });
    await loader.reload();

        // 恢复凭据指向的会话文件没了：不能悄悄开一个空会话（上下文会丢）。什么都没做就告诉 Go，让它把完整历史补过来重跑。
    if (spec.resume && !existsSync(spec.resume)) return { ...fail(`pi 的会话文件不见了：${spec.resume}`), resume_lost: true, resume: undefined };
    const sessionManager = spec.resume ? sdk.SessionManager.open(spec.resume) : sdk.SessionManager.create(spec.workspace);
    const created = await sdk.createAgentSession({
      cwd: spec.workspace,
      agentDir,
      modelRuntime,
      resourceLoader: loader,
      sessionManager,
      ...(model ? { model } : {}),
      ...(mode === "readonly" ? { tools: READ_ONLY_TOOLS } : {}),
    });
    session = created.session;
    if (!model && (await modelRuntime.getAvailable()).length === 0) return fail(NO_MODEL);
    if (cancelled) return tr.finish("cancelled", "", session.sessionFile ?? spec.resume ?? "");

    const unsubscribe = session.subscribe((ev) => tr.handle(ev));
    try {
      await session.prompt(spec.text);
    } finally {
      unsubscribe();
    }
    const resume = session.sessionFile ?? spec.resume ?? "";
    if (cancelled) return tr.finish("cancelled", "", resume);
    // pi 不抛模型错误（鉴权失败、额度用完…），错误在最后一条助手消息里。
    if (tr.lastError) return fail(tr.lastError, resume);
    return tr.finish("completed", "", resume);
  } catch (e) {
    const resume = session?.sessionFile ?? spec.resume ?? "";
    if (cancelled) return tr.finish("cancelled", "", resume);
    return fail(e instanceof Error ? e.message : String(e), resume);
  } finally {
    session?.dispose();
  }
}

export function servePi(io: WorkerIO, loadSdk: PiSdkLoader, exit: (code: number) => void): void {
  serveWorker(io, (spec, bridge) => runPi(spec, bridge, loadSdk), exit);
}
