// 按 seq 重新排序事件。
//
// 为什么需要：Go 端的事件总线保证 seq 单调递增，但传到前端的途中顺序不一定保持——
// Wails 开发服务器（wails dev 时用浏览器打开 http://localhost:34115）给每条消息开一个 goroutine 发送，
// 实测会出现 user_message 晚于 message_start 到达，界面上用户的话跑到了模型回答下面。
// 桌面窗口里事件经过主线程队列，一般是有序的；但前端不应该依赖传输层的这种细节。
//
// 做法：记住下一个应该到达的 seq；按序到达的立即交付，提前到达的先缓存，等缺口补上再一起交付。
// 如果缺口迟迟补不上（例如某条事件丢失），等待 maxWait 后按 seq 顺序把缓存全部交付，不会卡住界面。
// 第一条事件到达时还不知道它前面有没有更早的，所以先等一小会儿再定起点。

export interface Sequenced {
  seq: number;
}

export function createReorderBuffer<T extends Sequenced>(deliver: (ev: T) => void, maxWait = 150): (ev: T) => void {
  let next: number | null = null;
  const pending = new Map<number, T>();
  let timer: ReturnType<typeof setTimeout> | null = null;

  const drain = () => {
    while (next !== null && pending.has(next)) {
      const ev = pending.get(next)!;
      pending.delete(next);
      next++;
      deliver(ev);
    }
  };

  const flushAll = () => {
    timer = null;
    const seqs = [...pending.keys()].sort((a, b) => a - b);
    for (const s of seqs) deliver(pending.get(s)!);
    pending.clear();
    if (seqs.length) next = seqs[seqs.length - 1] + 1;
  };

  const schedule = () => {
    if (timer === null) timer = setTimeout(flushAll, maxWait);
  };

  return (ev: T) => {
    if (!ev || typeof ev.seq !== "number") {
      deliver(ev); // 没有 seq 的消息不参与排序
      return;
    }
    if (next !== null && ev.seq < next) {
      deliver(ev); // 迟到太久（已经越过它交付了）或重复：照常交付，由 reducer 自己处理
      return;
    }
    pending.set(ev.seq, ev);
    if (next === null) {
      schedule();
      return;
    }
    drain();
    if (pending.size > 0) schedule();
    else if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
  };
}
