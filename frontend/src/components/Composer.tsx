import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useChat } from "../store/chatStore";

// 输入框：Enter 发送，Shift+Enter 换行；运行中显示"停止"按钮。
export default function Composer() {
  const [text, setText] = useState("");
  const running = useChat((s) => s.running);
  const send = useChat((s) => s.send);
  const cancel = useChat((s) => s.cancel);
  const waiting = useChat((s) => s.permissions.length > 0);
  const ref = useRef<HTMLTextAreaElement>(null);

  // 运行中输入框是 disabled 的（防止边打字边按 Enter 误批准权限弹窗），浏览器会因此让它失去焦点。
  // 一轮结束、重新可用时把焦点还回来；只在焦点无处可去（落在 body 上）时才抢，不打断设置页等其他输入。
  useEffect(() => {
    const active = document.activeElement;
    if (!running && (!active || active === document.body)) ref.current?.focus();
  }, [running]);

  const submit = () => {
    const t = text.trim();
    if (!t || running) return;
    setText("");
    void send(t);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // isComposing：中文输入法选词时按下的 Enter 不应触发发送。
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <div className="shrink-0 border-t border-line px-6 pt-3">
      <div className="mx-auto flex max-w-3xl items-end gap-2 rounded-xl border border-line bg-panel px-3 py-2 focus-within:border-accent/60">
        <textarea
          ref={ref}
          autoFocus
          className="max-h-48 min-h-[24px] flex-1 resize-none bg-transparent py-1 text-[15px] outline-none placeholder:text-muted"
          rows={Math.min(8, Math.max(1, text.split("\n").length))}
          placeholder={running ? (waiting ? "等待你确认工具调用…" : "agent 正在工作…") : "输入消息，Enter 发送，Shift+Enter 换行"}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={running}
        />
        {running ? (
          <button className="rounded-lg border border-danger/50 px-3 py-1.5 text-sm text-danger hover:bg-danger/10" onClick={() => void cancel()}>
            停止
          </button>
        ) : (
          <button
            className="rounded-lg bg-accent px-3 py-1.5 text-sm font-medium text-white disabled:opacity-40"
            onClick={submit}
            disabled={!text.trim()}
          >
            发送
          </button>
        )}
      </div>
    </div>
  );
}
