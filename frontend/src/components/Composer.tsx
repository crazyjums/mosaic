import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";
import { isSendKey, MOD_KEY } from "../store/keys";
import { useUIPrefs } from "../store/uiPrefs";

// 输入框：按设置的快捷键发送；运行中显示"停止"按钮。
export default function Composer() {
  const [text, setText] = useState("");
  const running = useChat((s) => s.running);
  const send = useChat((s) => s.send);
  const cancel = useChat((s) => s.cancel);
  const waiting = useChat((s) => s.permissions.length > 0);
  const sendKey = useUIPrefs((s) => s.prefs.sendKey);
  const ref = useRef<HTMLTextAreaElement>(null);

  // 运行中输入框是 disabled 的（防止边打字边按 Enter 误批准权限弹窗），浏览器会因此让它失去焦点。
  // 一轮结束、重新可用时把焦点还回来；只在焦点无处可去（落在 body 上）时才抢，不打断设置页等其他输入。
  useEffect(() => {
    const active = document.activeElement;
    if (!running && (!active || active === document.body)) ref.current?.focus();
  }, [running]);

  // 高度跟着内容变化：按行数算不准（长句自动换行也会占多行），所以按 scrollHeight 算。
  // 先清掉高度再量，内容变少时才能缩回去；上限由 CSS 的 max-h 控制，超出后出现滚动条。
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  }, [text]);

  const submit = () => {
    const t = text.trim();
    if (!t || running) return;
    setText("");
    void send(t);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (isSendKey({ key: e.key, shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, metaKey: e.metaKey, isComposing: e.nativeEvent.isComposing }, sendKey)) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <div className="shrink-0 border-t border-line px-6 pt-3">
      <div className="mx-auto flex max-w-[var(--content-width)] items-end gap-2 rounded-xl border border-line bg-panel px-3 py-2 focus-within:border-accent/60">
        <textarea
          ref={ref}
          autoFocus
          className="max-h-60 min-h-[24px] flex-1 resize-none overflow-y-auto bg-transparent py-1 text-[15px] outline-none placeholder:text-muted"
          rows={1}
          placeholder={
            running
              ? waiting
                ? t("等待你确认工具调用…")
                : t("agent 正在工作…")
              : sendKey === "enter"
                ? t("输入消息，Enter 发送，Shift+Enter 换行")
                : t("输入消息，{key}+Enter 发送，Enter 换行", { key: MOD_KEY })
          }
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={running}
        />
        {running ? (
          <button className="rounded-lg border border-danger/50 px-3 py-1.5 text-sm text-danger hover:bg-danger/10" onClick={() => void cancel()}>
            {t("停止")}
          </button>
        ) : (
          <button
            className="rounded-lg bg-accent px-3 py-1.5 text-sm font-medium text-on-accent disabled:opacity-40"
            onClick={submit}
            disabled={!text.trim()}
          >
            {t("发送")}
          </button>
        )}
      </div>
    </div>
  );
}
