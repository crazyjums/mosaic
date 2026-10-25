import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";
import { isSendKey, MOD_KEY } from "../store/keys";
import { useSettings } from "../store/settingsStore";
import { useUIPrefs } from "../store/uiPrefs";

// 输入框：按设置的快捷键发送；运行中显示"停止"按钮。
export default function Composer() {
  const [text, setText] = useState("");
  const running = useChat((s) => s.running);
  const send = useChat((s) => s.send);
  const cancel = useChat((s) => s.cancel);
  const waiting = useChat((s) => s.permissions.length > 0);
  const sendKey = useUIPrefs((s) => s.prefs.sendKey);
  const openSettings = useSettings((s) => s.show);
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

  const hint =
    sendKey === "enter" ? t("Enter 发送 · Shift+Enter 换行") : t("{key}+Enter 发送 · Enter 换行", { key: MOD_KEY });

  return (
    <div className="shrink-0 px-6 pt-3">
      <div
        className={`mx-auto max-w-[var(--content-width)] rounded-card border bg-panel shadow-card transition-colors focus-within:border-accent focus-within:ring-2 focus-within:ring-[var(--focus-ring)] ${running ? "border-line opacity-95" : "border-line"}`}
      >
        <textarea
          ref={ref}
          autoFocus
          className="block max-h-60 min-h-[28px] w-full resize-none overflow-y-auto bg-transparent px-4 pb-1 pt-3 text-[15px] leading-relaxed outline-none placeholder:text-muted disabled:cursor-not-allowed"
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
          aria-label={t("消息输入框")}
        />
        <div className="flex items-center gap-2 px-3 pb-2 pt-1">
          <button className="rounded-ctl px-2 py-1 text-xs text-muted hover:bg-panel-2 hover:text-fg" onClick={() => void openSettings("permissions")} title={t("查看和管理 agent 可用的工具")}>
            🛠 {t("工具")}
          </button>
          <span className="text-[11px] text-muted">{hint}</span>
          <div className="flex-1" />
          {running ? (
            <button className="flex items-center gap-1.5 rounded-ctl border border-danger/50 px-3 py-1.5 text-sm text-danger hover:bg-danger/10 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-danger" onClick={() => void cancel()}>
              <span className="inline-block h-2.5 w-2.5 rounded-[2px] bg-current" aria-hidden="true" />
              {t("停止")}
            </button>
          ) : (
            <button className="btn-primary px-4" onClick={submit} disabled={!text.trim()}>
              {t("发送")} <span aria-hidden="true">↑</span>
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
