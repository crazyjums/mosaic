import type { AskRequest, FromWorker, Outcome, RunSpec, ToWorker } from "./workers/protocol";

// 管理引擎 worker（Claude Code、pi 的 SDK 各跑在一个 utilityProcess 里）：每一轮起一个 worker，
// 把它产生的事件和确认请求转给 Go 后端，把用户的回答和取消转回去。设计见 docs/design/harness.md。

/** 一个 worker 进程（Electron 的 utilityProcess，或测试里的 child_process）。 */
export interface WorkerHandle {
  post(msg: ToWorker): void;
  onMessage(cb: (msg: FromWorker) => void): void;
  /** 进程退出（无论正常与否）。 */
  onExit(cb: (code: number | null) => void): void;
  /** stderr 输出，出错时拼进错误信息。 */
  onStderr(cb: (chunk: string) => void): void;
  kill(): void;
}

/** 起一个 worker；这个引擎在当前环境不可用时返回 undefined。 */
export type SpawnWorker = (engine: string) => WorkerHandle | undefined;

/** 到 Go 后端的连接：harness.emit / harness.ask。 */
export interface GoLink {
  emit(runId: string, messageId: string, type: string, data: unknown): Promise<void>;
  ask(runId: string, req: AskRequest): Promise<"allow" | "allow_session" | "deny">;
}

/** 取消后给 worker 整理结果的时间，超过就强制结束。 */
const CANCEL_GRACE_MS = 8000;

interface Active {
  worker: WorkerHandle;
  cancel(): void;
}

export class HarnessManager {
  private active = new Map<string, Active>();

  constructor(
    private readonly spawn: SpawnWorker,
    private readonly go: GoLink,
    private readonly graceMs = CANCEL_GRACE_MS,
  ) {}

  /** 运行一轮，直到 worker 报告结束（或进程退出）。 */
  run(runId: string, spec: RunSpec): Promise<Outcome> {
    const worker = this.spawn(spec.engine);
    if (!worker) return Promise.resolve(errorOutcome(`引擎 ${spec.engine} 在当前环境不可用`));

    return new Promise<Outcome>((resolve) => {
      let settled = false;
      let cancelTimer: ReturnType<typeof setTimeout> | undefined;
      const stderr: string[] = [];
      // 事件必须按顺序送到 Go：每个 emit 接在上一个后面。结束前要等它们送完，否则 turn_end 会跑到事件前面。
      let chain: Promise<unknown> = Promise.resolve();
      const settle = (o: Outcome) => {
        if (settled) return;
        settled = true;
        if (cancelTimer) clearTimeout(cancelTimer);
        this.active.delete(runId);
        void chain.finally(() => resolve(o));
      };

      worker.onStderr((c) => {
        stderr.push(c);
        if (stderr.length > 30) stderr.shift();
      });
      worker.onMessage((m) => {
        switch (m.type) {
          case "emit":
            chain = chain.then(() => this.go.emit(runId, m.message_id, m.event_type, m.data)).catch(() => undefined);
            break;
          case "ask":
            // 确认要等用户，不能挡住后面的事件；回答回来时转给 worker。
            void chain
              .then(() => this.go.ask(runId, m.request))
              .catch((): "deny" => "deny")
              .then((decision) => worker.post({ type: "answer", id: m.id, decision }));
            break;
          case "done":
            settle(m.outcome);
            worker.kill();
            break;
        }
      });
      worker.onExit((code) => {
        // 没收到 done 就退出了：引擎崩溃。
        const tail = stderr.join("").trim().split("\n").slice(-6).join("\n");
        settle(errorOutcome(`引擎进程意外退出（${code ?? "被终止"}）${tail ? "\n" + tail : ""}`));
      });

      this.active.set(runId, {
        worker,
        cancel: () => {
          worker.post({ type: "cancel" });
          // 宽限期内没交回结果就强制结束，返回“已取消”。
          cancelTimer ??= setTimeout(() => {
            settle({ reason: "cancelled", usage: { input_tokens: 0, output_tokens: 0 } });
            worker.kill();
          }, this.graceMs);
        },
      });
      worker.post({ type: "run", spec });
    });
  }

  cancel(runId: string): void {
    this.active.get(runId)?.cancel();
  }

  /** 应用退出时：结束所有还在跑的 worker。 */
  killAll(): void {
    for (const a of this.active.values()) a.worker.kill();
    this.active.clear();
  }
}

function errorOutcome(error: string): Outcome {
  return { reason: "error", error, usage: { input_tokens: 0, output_tokens: 0 } };
}
