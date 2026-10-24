import { useEffect } from "react";
import type { Decision } from "../protocol/events";
import { useChat } from "../store/chatStore";
import { t } from "../i18n";

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

  const answer = (d: Decision) => req && void respond(req.request_id, d);

  // 键盘快捷键：Enter 允许，Esc 拒绝。
  useEffect(() => {
    if (!req) return;
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
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-6">
      <div className="w-full max-w-lg rounded-xl border border-line bg-panel shadow-2xl">
        <div className="border-b border-line px-5 py-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-warn">
            <span>⚠</span> {t("需要你的确认")}
          </div>
          <div className="mt-2 text-[15px]">{req.summary}</div>
          <div className="mt-1 text-xs text-muted">
            {t("工具")} <span className="font-mono">{req.tool}</span> · {RISK_TEXT[req.risk] ? t(RISK_TEXT[req.risk]) : req.risk}
          </div>
        </div>
        <pre className="max-h-64 overflow-auto whitespace-pre-wrap px-5 py-3 font-mono text-xs text-muted">
          {JSON.stringify(req.input, null, 2)}
        </pre>
        <div className="flex justify-end gap-2 border-t border-line px-5 py-3 text-sm">
          <button className="rounded-md px-3 py-1.5 text-danger hover:bg-danger/10" onClick={() => answer("deny")}>
            {t("拒绝")} <kbd className="ml-1 text-[10px] opacity-60">Esc</kbd>
          </button>
          <button className="rounded-md border border-line px-3 py-1.5 hover:bg-panel-2" onClick={() => answer("allow_session")}>
            {t("本会话总是允许")}
          </button>
          <button className="rounded-md bg-accent px-3 py-1.5 font-medium text-white hover:opacity-90" onClick={() => answer("allow")}>
            {t("允许")} <kbd className="ml-1 text-[10px] opacity-70">Enter</kbd>
          </button>
        </div>
      </div>
    </div>
  );
}
