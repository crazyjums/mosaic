// 根据后端推来的事件，跟踪哪些会话正在跑，并决定该不该弹系统通知（迁移阶段 4）。
// 纯逻辑、不依赖 electron，方便单测；真正弹通知、显示窗口在 main.ts。

export interface BackendEvent {
  type?: string;
  session_id?: string;
  data?: Record<string, unknown>;
}

export type Alert = { kind: "done"; sessionId: string; failed: boolean } | { kind: "permission"; sessionId: string; tool: string };

export class Activity {
  private running = new Set<string>();

  /** 有多少个会话正在跑。 */
  get count(): number {
    return this.running.size;
  }

  /** 消化一个事件；需要提醒用户时返回提醒内容。 */
  handle(ev: unknown): Alert | undefined {
    const e = ev as BackendEvent | null;
    if (!e || typeof e.type !== "string" || typeof e.session_id !== "string" || !e.session_id) return undefined;
    switch (e.type) {
      case "turn_start":
        this.running.add(e.session_id);
        return undefined;
      case "turn_end": {
        this.running.delete(e.session_id);
        const reason = e.data?.reason;
        // 用户自己取消的不用通知；出错和跑到步数上限要让人知道。
        if (reason === "cancelled") return undefined;
        return { kind: "done", sessionId: e.session_id, failed: reason === "error" || reason === "max_steps" };
      }
      case "permission_request":
        return { kind: "permission", sessionId: e.session_id, tool: typeof e.data?.tool === "string" ? e.data.tool : "" };
      default:
        return undefined;
    }
  }
}

/** 通知文案。不放对话内容：通知会出现在锁屏和通知中心里。 */
export function alertText(a: Alert): { title: string; body: string } {
  if (a.kind === "permission") return { title: "Mosaic 需要你确认", body: a.tool ? `agent 想使用工具 ${a.tool}` : "agent 在等你确认一个操作" };
  return a.failed ? { title: "Mosaic：任务没有顺利完成", body: "点击查看详情" } : { title: "Mosaic：任务完成", body: "点击查看结果" };
}
