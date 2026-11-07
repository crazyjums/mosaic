import { existsSync, statSync } from "node:fs";
import { delimiter, join } from "node:path";
import { ClaudeTranslator, riskOf, type SdkMessage } from "./cc-translate";
import type { AskRequest, FromWorker, Outcome, RunSpec, ToWorker, WorkerIO } from "./protocol";

// Claude Code 引擎（Claude Agent SDK）。跑在 Electron 的 utilityProcess 里，和主进程只靠 protocol.ts 的消息通信。
//
// 登录和模型都是用户自己的 Claude Code：SDK 启动用户装的 `claude` 命令（不打包——SDK 自带的原生可执行文件有 300MB 以上），
// 凭证来自用户的 Claude Code 登录或环境里的 ANTHROPIC_API_KEY。Mosaic 不读也不保存它们。
// 请按 Anthropic 的使用条款使用；第三方应用通常应使用 API Key 方式认证。

export interface SdkQuery extends AsyncIterable<SdkMessage> {
  close?(): void;
}
export interface Sdk {
  query(p: { prompt: string; options: Record<string, unknown> }): SdkQuery;
}
export type SdkLoader = () => Promise<Sdk>;

const MISSING_CLAUDE = "没有找到 Claude Code 命令 claude。请先安装 Claude Code（npm install -g @anthropic-ai/claude-code）并登录，或在厂商配置档的选项里指定 command";

/** 在 PATH 里找 claude 可执行文件（Windows 上按 PATHEXT 找，优先 .exe）。 */
export function findClaude(env: NodeJS.ProcessEnv = process.env, platform: NodeJS.Platform = process.platform): string | undefined {
  const exts = platform === "win32" ? [".exe", ".cmd", ".bat", ""] : [""];
  for (const dir of (env.PATH ?? env.Path ?? "").split(delimiter)) {
    if (!dir) continue;
    for (const ext of exts) {
      const p = join(dir, "claude" + ext);
      try {
        if (existsSync(p) && statSync(p).isFile()) return p;
      } catch {
        // 无权限访问的目录跳过
      }
    }
  }
  return undefined;
}

/** Mosaic 的权限模式 → SDK 的权限模式。 */
export function sdkPermission(mode: string): { permissionMode: string; allowDangerouslySkipPermissions?: boolean } {
  switch (mode) {
    case "readonly":
      return { permissionMode: "plan" }; // 规划模式：可以读、不能改、不能执行
    case "accept_edits": // Mosaic 的取值（permission.ModeAcceptEdits）；SDK 那边叫 acceptEdits
      return { permissionMode: "acceptEdits" };
    case "auto":
      return { permissionMode: "bypassPermissions", allowDangerouslySkipPermissions: true }; // 与 Mosaic 自动模式“放行一切”一致
    default:
      return { permissionMode: "default" };
  }
}

function summarize(tool: string, input: Record<string, unknown>): string {
  const pick = (k: string) => (typeof input[k] === "string" ? (input[k] as string) : "");
  const detail = pick("command") || pick("file_path") || pick("path") || pick("url") || pick("pattern") || pick("query");
  const text = detail ? `${tool}：${detail}` : tool;
  return text.length > 200 ? text.slice(0, 200) + "…" : text;
}

type Decision = "allow" | "allow_session" | "deny";

/** 运行一轮。io 是和主进程之间的通道；loadSdk 在测试里可以换成假的。 */
export async function runClaudeCode(spec: RunSpec, io: WorkerIO, loadSdk: SdkLoader, which: () => string | undefined = findClaude): Promise<Outcome> {
  const tr = new ClaudeTranslator({ emit: (message_id, event_type, data) => io.post({ type: "emit", message_id, event_type, data }) }, spec.profile || "claudecode", spec.model);
  const fail = (error: string) => tr.finish("error", error);

  const command = typeof spec.options?.command === "string" && spec.options.command.trim() ? spec.options.command.trim() : which();
  if (!command) return fail(MISSING_CLAUDE);
  let sdk: Sdk;
  try {
    sdk = await loadSdk();
  } catch (e) {
    return fail(`无法加载 Claude Agent SDK：${e instanceof Error ? e.message : String(e)}`);
  }

  const ac = new AbortController();
  let cancelled = false;
  let q: SdkQuery | undefined;
  const pendingAsks = new Map<number, (d: Decision) => void>();
  let askSeq = 0;
  io.onMessage((m: ToWorker) => {
    if (m.type === "cancel") {
      cancelled = true;
      ac.abort();
      q?.close?.();
      for (const resolve of pendingAsks.values()) resolve("deny"); // 等待中的确认按拒绝处理
      pendingAsks.clear();
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

  const stderrTail: string[] = [];
  const perm = sdkPermission(spec.permission_mode);
  const options: Record<string, unknown> = {
    cwd: spec.workspace,
    pathToClaudeCodeExecutable: command,
    abortController: ac,
    includePartialMessages: true,
    ...perm,
    stderr: (d: string) => {
      stderrTail.push(d);
      if (stderrTail.length > 20) stderrTail.shift();
    },
    canUseTool: async (name: string, input: Record<string, unknown>, opts: { toolUseID?: string; title?: string; suggestions?: unknown }) => {
      const decision = await ask({
        call_id: opts.toolUseID ?? "",
        tool: name,
        risk: riskOf(name),
        summary: opts.title || summarize(name, input),
        input,
      });
      if (decision === "deny") return { behavior: "deny", message: cancelled ? "用户停止了这一轮" : "用户拒绝了这次操作", interrupt: cancelled };
      // “本会话允许”：把 SDK 建议的权限更新交回去，同类操作之后不再询问。
      return { behavior: "allow", updatedInput: input, ...(decision === "allow_session" && opts.suggestions ? { updatedPermissions: opts.suggestions } : {}) };
    },
  };
  if (spec.model && spec.model !== "default") options.model = spec.model;
  if (spec.resume) options.resume = spec.resume;

  try {
    q = sdk.query({ prompt: spec.text, options });
    for await (const m of q) tr.handle(m);
  } catch (e) {
    if (cancelled) return tr.finish("cancelled");
    const tail = stderrTail.join("").trim().split("\n").slice(-6).join("\n");
    return fail(`${e instanceof Error ? e.message : String(e)}${tail ? "\n" + tail : ""}`);
  }
  if (cancelled) return tr.finish("cancelled");
  if (!tr.result) return fail("Claude Code 在给出结果之前就结束了");
  return tr.result.ok ? tr.finish("completed") : fail(tr.result.error);
}

/** 进程入口：在 utilityProcess 里等主进程发来 run，跑完回 done 并退出。 */
export function serve(io: WorkerIO, loadSdk: SdkLoader, exit: (code: number) => void): void {
  let started = false;
  const inbox: ToWorker[] = [];
  let forward: ((m: ToWorker) => void) | undefined;
  io.onMessage((m) => {
    if (m.type === "run" && !started) {
      started = true;
      const bridge: WorkerIO = {
        post: (msg: FromWorker) => io.post(msg),
        onMessage: (cb) => {
          forward = cb;
          for (const queued of inbox.splice(0)) cb(queued); // run 之前就到的消息
        },
      };
      runClaudeCode(m.spec, bridge, loadSdk)
        .catch((e): Outcome => ({ reason: "error", error: e instanceof Error ? e.message : String(e), usage: { input_tokens: 0, output_tokens: 0 } }))
        .then((outcome) => {
          io.post({ type: "done", outcome });
          setTimeout(() => exit(0), 50); // 给 done 一点时间送出去
        });
    } else if (forward) forward(m);
    else inbox.push(m);
  });
}
