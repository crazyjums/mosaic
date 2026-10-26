import { useEffect, useState } from "react";
import type { Decision } from "../protocol/events";
import { useChat } from "../store/chatStore";
import { t } from "../i18n";
import MosaicMark from "./MosaicMark";

// 中文原文，渲染时翻译。
const RISK_TEXT: Record<string, string> = {
  write: "将修改工作区中的文件",
  exec: "将在你的电脑上执行命令",
};

// 权限确认弹窗。agent 循环此时在后端阻塞等待，直到这里调用 respondPermission。
// 多个请求排队时一次只显示第一个（内核是顺序执行工具的，通常只会有一个）。
export default function PermissionDialog() {
  const req = useChat((s) => s.permissions[0]);
  const respond = useChat((s) => s.respondPermission);
  // “稍后处理”：多个会话同时运行时，用户可能想先去看别的会话。弹窗收起成一条不挡操作的提示条，请求仍在等待。
  const [snoozed, setSnoozed] = useState<string | null>(null);
  const hidden = !!req && snoozed === req.request_id;

  const answer = (d: Decision) => req && void respond(req.request_id, d);

  // 键盘快捷键：Enter 允许，Esc 拒绝。
  useEffect(() => {
    if (!req || hidden) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") answer("deny");
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        answer("allow");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (!req) return null;
  if (hidden) {
    return (
      <div className="fixed bottom-24 left-1/2 z-30 flex max-w-[90vw] -translate-x-1/2 items-center gap-3 rounded-full border border-warn/40 bg-panel px-4 py-2 text-sm shadow-pop" role="status">
        <MosaicMark size="xs" state="problem" />
        <span className="min-w-0 truncate">{t("有一个权限请求在等你：{summary}", { summary: req.summary })}</span>
        <button className="btn-primary shrink-0 px-3 py-1 text-xs" onClick={() => setSnoozed(null)}>
          {t("处理")}
        </button>
      </div>
    );
  }
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-6" role="alertdialog" aria-modal="true" aria-label={t("需要你的确认")}>
      <div className="shadow-pop w-full max-w-lg rounded-modal border border-line bg-panel">
        <div className="border-b border-line px-5 py-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-warn">
            <MosaicMark size="xs" state="problem" /> {t("需要你的确认")}
          </div>
          <div className="mt-2 text-[15px]">{req.summary}</div>
          <div className="mt-1 text-xs text-muted">
            {t("工具")} <span className="font-mono">{req.tool}</span> · {RISK_TEXT[req.risk] ? t(RISK_TEXT[req.risk]) : req.risk}
          </div>
        </div>
        {req.sensitive && <div className="border-b border-line bg-warn/10 px-5 py-2 text-xs text-warn">{t("这个工具会读取私密内容（例如文档、聊天记录），读到的内容会发给你选择的模型厂商；每次都需要你确认。")}</div>}
        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words bg-code px-5 py-3 font-mono text-xs text-muted">
          {JSON.stringify(req.input, null, 2)}
        </pre>
        <div className="flex justify-end gap-2 border-t border-line px-5 py-3 text-sm">
          <button className="mr-auto rounded-ctl px-3 py-1.5 text-muted hover:bg-panel-2 hover:text-fg" onClick={() => setSnoozed(req.request_id)} title={t("先去看别的会话，这个请求继续等你")}>
            {t("稍后处理")}
          </button>
          <button className="rounded-ctl px-3 py-1.5 text-danger hover:bg-danger/10 focus-visible:outline-2 focus-visible:outline-danger" onClick={() => answer("deny")}>
            {t("拒绝")} <kbd className="ml-1 text-[10px] opacity-60">Esc</kbd>
          </button>
          {!req.sensitive && (
            <button className="btn" onClick={() => answer("allow_session")}>
              {t("本会话总是允许")}
            </button>
          )}
          <button className="btn-primary" onClick={() => answer("allow")}>
            {t("允许")} <kbd className="ml-1 text-[10px] opacity-70">Enter</kbd>
          </button>
        </div>
      </div>
    </div>
  );
}
