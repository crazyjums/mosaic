import { utilityProcess } from "electron";
import { existsSync } from "node:fs";
import { join } from "node:path";
import type { SpawnWorker, WorkerHandle } from "./harness";
import type { FromWorker, ToWorker } from "./workers/protocol";

// 把 Electron 的 utilityProcess 包成 WorkerHandle。单独一个文件：harness.ts 不 import electron，能在普通 Node 里测试。

/** 每个引擎对应的 worker 入口（相对 dist/src/workers/）。pi 在后面的提交里加。 */
const ENTRIES: Record<string, string> = { claudecode: "claudecode-main.js" };

/**
 * 打包后 worker 和 SDK 放在 app.asar.unpacked 里（见 electron-builder.yml 的 asarUnpack）：
 * utilityProcess 要像普通进程一样 import 它们，不依赖 asar 的虚拟文件系统。
 */
export function workerPath(dir: string, entry: string): string {
  return join(dir, "workers", entry).replace(/app\.asar([\\/])/, "app.asar.unpacked$1");
}

/** 当前环境能提供的引擎（worker 文件存在）。 */
export function availableEngines(dir: string): string[] {
  return Object.keys(ENTRIES).filter((e) => existsSync(workerPath(dir, ENTRIES[e])));
}

export function electronSpawner(dir: string): SpawnWorker {
  return (engine) => {
    const entry = ENTRIES[engine];
    if (!entry) return undefined;
    const file = workerPath(dir, entry);
    if (!existsSync(file)) return undefined;
    const child = utilityProcess.fork(file, [], { serviceName: `mosaic-${engine}`, stdio: "pipe" });
    const handle: WorkerHandle = {
      post: (m: ToWorker) => child.postMessage(m),
      onMessage: (cb) => child.on("message", (m: unknown) => cb(m as FromWorker)),
      onExit: (cb) => child.on("exit", (code) => cb(code)),
      onStderr: (cb) => child.stderr?.on("data", (d: Buffer) => cb(d.toString("utf8"))),
      kill: () => void child.kill(),
    };
    return handle;
  };
}
