import type { FromWorker, Outcome, RunSpec, ToWorker, WorkerIO } from "./protocol";

/**
 * 进程入口（各引擎共用）：在 utilityProcess 里等主进程发来 run，跑完回 done 并退出。
 * run 在 worker 里执行一轮；io 是它和主进程之间的通道（run 之前就到的消息会先排队）。
 */
export function serveWorker(io: WorkerIO, run: (spec: RunSpec, io: WorkerIO) => Promise<Outcome>, exit: (code: number) => void): void {
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
      run(m.spec, bridge)
        .catch((e): Outcome => ({ reason: "error", error: e instanceof Error ? e.message : String(e), usage: { input_tokens: 0, output_tokens: 0 } }))
        .then((outcome) => {
          io.post({ type: "done", outcome });
          setTimeout(() => exit(0), 50); // 给 done 一点时间送出去
        });
    } else if (forward) forward(m);
    else inbox.push(m);
  });
}
