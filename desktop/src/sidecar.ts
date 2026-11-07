import { ChildProcess, spawn, spawnSync } from "node:child_process";
import { EventEmitter } from "node:events";
import { Peer, type Handler } from "./rpc";

export interface SidecarOptions {
  command: string;
  args: string[];
  cwd?: string;
  env?: NodeJS.ProcessEnv;
  /** 后端要壳做的事（host.*）。 */
  onHost: Handler;
}

export interface Hello {
  protocol: number;
  event_protocol: number;
  version: string;
  methods: string[];
}

/** 壳和后端 mosaicd 约定的协议版本；后端的 protocol 不同就拒绝连接。 */
export const PROTOCOL = 1;

/**
 * Go 后端子进程：拉起、握手、转发调用、把后端推来的事件抛出去；退出时先关 stdin 让它自己收尾，超时再强杀。
 *
 * 事件：'event'（后端推来的 agent 事件）、'exit'（子进程退出，带 code 和是否是我们主动停的）。
 */
export class Sidecar extends EventEmitter {
  private child?: ChildProcess;
  private peer?: Peer;
  private allowed = new Set<string>();
  private stopping = false;
  hello?: Hello;

  constructor(private readonly opts: SidecarOptions) {
    super();
  }

  async start(): Promise<Hello> {
    this.stopping = false;
    const child = spawn(this.opts.command, this.opts.args, {
      cwd: this.opts.cwd,
      env: this.opts.env ?? process.env,
      stdio: ["pipe", "pipe", "inherit"], // stderr 是后端日志，直接进壳的日志
      windowsHide: true, // 不弹控制台窗口
    });
    this.child = child;
    const peer = new Peer(
      (line) => child.stdin?.write(line),
      (method, params) => {
        if (method === "event") {
          this.emit("event", (params as { data: unknown }).data);
          return null;
        }
        return this.opts.onHost(method, params);
      },
    );
    this.peer = peer;
    child.stdout!.setEncoding("utf8");
    child.stdout!.on("data", (chunk: string) => peer.feed(chunk));
    child.stdin!.on("error", () => {}); // 子进程退出后再写会 EPIPE，不要让它变成未捕获异常
    child.on("error", (err) => {
      peer.close(`无法启动后端：${err.message}`);
      this.emit("exit", { code: null, expected: false, error: err });
    });
    child.on("exit", (code, signal) => {
      peer.close();
      this.emit("exit", { code, signal, expected: this.stopping });
    });

    const hello = await peer.call<Hello>("rpc.hello");
    if (hello.protocol !== PROTOCOL) {
      await this.stop();
      throw new Error(`后端协议版本 ${hello.protocol} 和外壳要求的 ${PROTOCOL} 不一致，请重新安装`);
    }
    this.hello = hello;
    this.allowed = new Set(hello.methods);
    return hello;
  }

  /** 调用后端的绑定方法。只放行握手时后端报告的方法：渲染进程传来的方法名不能直接信任。 */
  call(method: string, args: unknown[]): Promise<unknown> {
    if (!this.peer) return Promise.reject(new Error("后端没有运行"));
    if (!this.allowed.has(method)) return Promise.reject(new Error(`没有方法 ${method}`));
    return this.peer.call(method, args);
  }

  /**
   * 主进程自己调后端的专用方法（harness.*）：不走渲染进程的白名单，也不会出现在握手的方法列表里。
   * 渲染进程拿不到这个函数，所以它们调不到。
   */
  callInternal<T = unknown>(method: string, params: unknown): Promise<T> {
    if (!this.peer) return Promise.reject(new Error("后端没有运行"));
    return this.peer.call<T>(method, params);
  }

  get running(): boolean {
    return !!this.child && this.child.exitCode === null && !this.child.killed;
  }

  /** 正常停止：关 stdin（后端读到 EOF 会取消会话并退出），最多等 timeoutMs，还不退就强杀（Windows 上连子进程一起）。 */
  async stop(timeoutMs = 5000): Promise<void> {
    const child = this.child;
    if (!child || child.exitCode !== null) return;
    this.stopping = true;
    const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
    child.stdin?.end();
    const timer = setTimeout(() => forceKill(child), timeoutMs);
    await exited;
    clearTimeout(timer);
  }
}

function forceKill(child: ChildProcess): void {
  if (child.pid === undefined) return;
  if (process.platform === "win32") {
    // 后端启动的 MCP 等子进程也要一起结束；Node 没有 Job Object，用 taskkill 杀进程树。
    spawnSync("taskkill", ["/pid", String(child.pid), "/T", "/F"], { windowsHide: true });
  } else {
    child.kill("SIGKILL");
  }
}
