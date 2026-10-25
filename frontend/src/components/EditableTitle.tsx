import { useEffect, useRef, useState } from "react";
import { t } from "../i18n";

// 可以就地改名的标题：双击（或点外面的 ✎ 按钮，由 editing 控制）进入编辑，
// Enter 保存、Esc 取消、失去焦点保存。会话栏和顶部标题栏共用。
export default function EditableTitle({
  value,
  placeholder,
  editing,
  onEditingChange,
  onSave,
  className = "",
}: {
  value: string;
  placeholder: string;
  editing: boolean;
  onEditingChange: (v: boolean) => void;
  onSave: (title: string) => unknown;
  className?: string;
}) {
  const [draft, setDraft] = useState(value);
  const ref = useRef<HTMLInputElement>(null);
  // 防止 Enter 保存后紧接着的 blur 再保存一次。
  const done = useRef(false);

  useEffect(() => {
    if (editing) {
      setDraft(value);
      done.current = false;
      requestAnimationFrame(() => ref.current?.select());
    }
  }, [editing, value]);

  const finish = (save: boolean) => {
    if (done.current) return;
    done.current = true;
    onEditingChange(false);
    const next = draft.trim();
    if (save && next && next !== value) void onSave(next);
  };

  if (!editing) {
    return (
      <span className={`truncate ${className}`} onDoubleClick={() => onEditingChange(true)} title={t("双击改名")}>
        {value || placeholder}
      </span>
    );
  }
  return (
    <input
      ref={ref}
      className={`input min-w-0 flex-1 px-1.5 py-0.5 text-sm ${className}`}
      value={draft}
      maxLength={100}
      aria-label={t("会话名称")}
      onChange={(e) => setDraft(e.target.value)}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        if (e.key === "Enter" && !e.nativeEvent.isComposing) finish(true);
        else if (e.key === "Escape") finish(false);
      }}
      onBlur={() => finish(true)}
    />
  );
}
