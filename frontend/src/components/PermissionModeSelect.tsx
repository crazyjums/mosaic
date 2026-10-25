import { useEffect, useRef, useState } from "react";
import type { PermissionMode } from "../bridge";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";

// 权限档位（参考 Claude Code 的权限模式），从保守到放开。与 Go 端 permission.Modes() 一一对应。
// label / desc 是中文原文，渲染时用 t() 翻译。
export const MODES: { value: PermissionMode; label: string; desc: string }[] = [
  { value: "readonly", label: "只读", desc: "只能读文件和列目录，写文件、执行命令一律拒绝" },
  { value: "default", label: "默认", desc: "读操作直接放行，写文件和执行命令前询问" },
  { value: "accept_edits", label: "自动接受编辑", desc: "读写文件直接放行，执行命令前仍询问" },
  { value: "auto", label: "全部放行", desc: "所有操作都不再询问，包括执行任意命令" },
];

const TONE: Record<PermissionMode, string> = {
  readonly: "border-line text-muted",
  default: "border-line text-fg",
  accept_edits: "border-warn/50 text-warn",
  auto: "border-danger/60 bg-danger/10 text-danger",
};

// 状态栏里的权限档位选择器。切到"全部放行"前弹出危险确认，其他档位直接生效。
export default function PermissionModeSelect() {
  const mode = useChat((s) => s.permissionMode);
  const setMode = useChat((s) => s.setPermissionMode);
  const [confirming, setConfirming] = useState(false);

  const onChange = (m: PermissionMode) => {
    if (m === "auto") setConfirming(true);
    else void setMode(m);
  };

  return (
    <>
      <select
        className={`shrink-0 rounded border px-1.5 py-0.5 ${TONE[mode] ?? TONE.default} bg-panel`}
        value={mode}
        onChange={(e) => onChange(e.target.value as PermissionMode)}
        title={t("权限：{desc}（立即生效，会被记住；全部放行除外）", { desc: t(MODES.find((m) => m.value === mode)?.desc ?? "") })}
        aria-label={t("权限档位")}
      >
        {MODES.map((m) => (
          <option key={m.value} value={m.value}>
            {m.value === "auto" ? "⚠ " : ""}
            {t("权限：{label}", { label: t(m.label) })}
          </option>
        ))}
      </select>
      {confirming && (
        <DangerConfirm
          onCancel={() => setConfirming(false)}
          onConfirm={() => {
            setConfirming(false);
            void setMode("auto");
          }}
        />
      )}
    </>
  );
}

// "全部放行"的危险确认。刻意不提供 Enter 快捷确认，默认焦点在"取消"上，Esc 取消。
function DangerConfirm({ onCancel, onConfirm }: { onCancel: () => void; onConfirm: () => void }) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  // 用 ref 取最新的回调，effect 只在打开时执行一次：流式输出时状态栏会频繁重渲染，不能每次都把焦点抢回来。
  const cancel = useRef(onCancel);
  cancel.current = onCancel;
  useEffect(() => {
    cancelRef.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && cancel.current();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6" role="alertdialog" aria-modal="true" aria-labelledby="danger-title">
      <div className="shadow-pop w-full max-w-md rounded-modal border border-danger/50 bg-panel">
        <div className="px-5 py-4">
          <div id="danger-title" className="flex items-center gap-2 text-sm font-semibold text-danger">
            <span>⚠</span> {t("开启「全部放行」？")}
          </div>
          <p className="mt-3 text-sm leading-relaxed text-fg">
            {t("开启后，agent 写文件、执行任意 shell 命令都不再经过你的确认。模型可能误删文件、改坏代码，或运行你没有预料到的命令。")}
          </p>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-xs text-muted">
            <li>{t("只建议在可以随意破坏的目录（一次性的沙箱、已提交的 git 仓库）里使用")}</li>
            <li>{t("随时可以在状态栏切回「默认」，立即生效")}</li>
            <li>{t("只对本次运行有效，不会被记住；重启应用后回到切换前的档位")}</li>
          </ul>
        </div>
        <div className="flex justify-end gap-2 border-t border-line px-5 py-3 text-sm">
          <button ref={cancelRef} className="rounded-md border border-line px-3 py-1.5 hover:bg-panel-2" onClick={onCancel}>
            {t("取消")} <kbd className="ml-1 text-[10px] opacity-60">Esc</kbd>
          </button>
          <button className="rounded-md bg-danger px-3 py-1.5 font-medium text-white hover:opacity-90" onClick={onConfirm}>
            {t("我了解风险，全部放行")}
          </button>
        </div>
      </div>
    </div>
  );
}
