import { pathToFileURL } from "node:url";
import { findPiEntry } from "./pi-locate";
import { servePi } from "./pi";
import type { FromWorker, ToWorker, WorkerIO } from "./protocol";

// utilityProcess 的入口：process.parentPort 是和主进程之间的通道。
// SDK 是只有 ESM 的包，这里用动态 import() 加载（本文件编译成 CommonJS）。
const parentPort = (process as unknown as { parentPort?: { postMessage(m: unknown): void; on(ev: "message", cb: (e: { data: unknown }) => void): void } }).parentPort;

const io: WorkerIO = parentPort
  ? {
      post: (m: FromWorker) => parentPort.postMessage(m),
      onMessage: (cb) => parentPort.on("message", (e) => cb(e.data as ToWorker)),
    }
  : {
      // 没有 parentPort 时按 Node 的 IPC 通道（child_process.fork）工作：测试和排查问题时用。
      post: (m: FromWorker) => process.send?.(m),
      onMessage: (cb) => process.on("message", (m) => cb(m as ToWorker)),
    };

servePi(
  io,
  async () => {
    const entry = findPiEntry(__dirname, (process as unknown as { resourcesPath?: string }).resourcesPath);
    if (!entry) throw new Error("找不到 pi SDK（resources/pi/index.js）");
    return (await import(pathToFileURL(entry).href)) as never;
  },
  (code) => process.exit(code),
);
