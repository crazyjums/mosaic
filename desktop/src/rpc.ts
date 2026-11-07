// stdio 上的 JSON-RPC 对等端（和 Go 端 internal/rpc 对应）：一行一条消息，只按 "\n" 切分。
//
// 为什么不用 readline：它会把 JSON 字符串里的 U+2028 / U+2029 也当成换行，把一条消息切成两半。

export interface RpcError {
  code: number;
  message: string;
}

export class RemoteError extends Error {
  constructor(
    message: string,
    readonly code: number,
  ) {
    super(message);
  }
}

interface Message {
  jsonrpc?: string;
  id?: string | number | null;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: RpcError;
}

/** 处理对方发来的请求（有 id，要回应）或通知（没有 id）；返回值编码进回应，抛出的错误变成错误回应。 */
export type Handler = (method: string, params: unknown) => unknown | Promise<unknown>;

export class Peer {
  private nextId = 1;
  private buf = "";
  private pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();
  private closed = false;

  constructor(
    private readonly write: (line: string) => void,
    private readonly handler: Handler,
  ) {}

  /** 喂入从对方收到的数据（可以是任意切分的片段）。 */
  feed(chunk: string): void {
    this.buf += chunk;
    let i: number;
    while ((i = this.buf.indexOf("\n")) >= 0) {
      const line = this.buf.slice(0, i);
      this.buf = this.buf.slice(i + 1);
      if (line.trim()) this.onLine(line);
    }
  }

  call<T = unknown>(method: string, params: unknown[] = []): Promise<T> {
    if (this.closed) return Promise.reject(new Error("后端连接已断开"));
    const id = this.nextId++;
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject });
      this.send({ id, method, params });
    });
  }

  notify(method: string, params: unknown): void {
    if (!this.closed) this.send({ method, params });
  }

  /** 连接断开：所有等待中的调用立刻失败。 */
  close(reason = "后端连接已断开"): void {
    this.closed = true;
    for (const p of this.pending.values()) p.reject(new Error(reason));
    this.pending.clear();
  }

  private send(m: Message): void {
    this.write(JSON.stringify({ jsonrpc: "2.0", ...m }) + "\n");
  }

  private onLine(line: string): void {
    let m: Message;
    try {
      m = JSON.parse(line) as Message;
    } catch {
      return; // 不是协议消息（不应该出现，stdout 只写协议）：忽略
    }
    if (m.method === undefined) {
      // 对我们请求的回应。后端的 id 是数字（我们发出去的）。
      const p = typeof m.id === "number" ? this.pending.get(m.id) : undefined;
      if (!p || typeof m.id !== "number") return;
      this.pending.delete(m.id);
      if (m.error) p.reject(new RemoteError(m.error.message, m.error.code));
      else p.resolve(m.result);
      return;
    }
    const isCall = m.id !== undefined && m.id !== null;
    Promise.resolve()
      .then(() => this.handler(m.method!, m.params))
      .then(
        (result) => isCall && this.send({ id: m.id, result: result ?? null }),
        (err: unknown) => isCall && this.send({ id: m.id, error: { code: -32000, message: err instanceof Error ? err.message : String(err) } }),
      );
  }
}
