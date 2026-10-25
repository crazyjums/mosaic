import { useEffect, useMemo, useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent } from "react";
import { backend, type SkillInfo } from "../bridge";
import { t } from "../i18n";
import { ACCEPT, checkAdd, fmtSize, readAttachment, type PendingAttachment } from "../store/attachments";
import { useChat } from "../store/chatStore";
import { isSendKey, MOD_KEY } from "../store/keys";
import { filterSkills, skillText, slashQuery } from "../store/slash";
import { useSettings } from "../store/settingsStore";
import { useUIPrefs } from "../store/uiPrefs";
import QueuePanel from "./QueuePanel";

// 输入框：按设置的快捷键发送；可以添加图片和文本附件（选择 / 粘贴 / 拖进来）；键入 / 弹出技能菜单。
//
// 运行中输入框仍然可用：这时发送的消息进入待发送队列（见 store/queue.ts），这一轮结束后按顺序发出。
// 唯一例外是在等你确认权限时：输入框暂时禁用，免得边打字边按 Enter 误批准权限弹窗。
export default function Composer() {
  const [text, setText] = useState("");
  const [pending, setPending] = useState<PendingAttachment[]>([]);
  const [attachError, setAttachError] = useState("");
  const [dragging, setDragging] = useState(false);
  const running = useChat((s) => s.running);
  const send = useChat((s) => s.send);
  const cancel = useChat((s) => s.cancel);
  const waiting = useChat((s) => s.permissions.length > 0);
  const queued = useChat((s) => s.queue.filter((m) => m.sessionId === s.sessionId).length);
  const sendKey = useUIPrefs((s) => s.prefs.sendKey);
  const openSettings = useSettings((s) => s.show);
  const ref = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  // 键入 / 时弹出技能菜单。技能随工作区变化，所以每次开始键入 / 时重新取一次。
  const query = slashQuery(text);
  const [skills, setSkills] = useState<SkillInfo[] | null>(null);
  const [active, setActive] = useState(0);
  const [dismissed, setDismissed] = useState(false); // 用户按了 Esc：这一段 /xxx 不再弹菜单
  const menuOpen = query !== null && !dismissed;
  useEffect(() => {
    if (query === "") {
      setDismissed(false);
      void backend().listSkills().then(setSkills).catch(() => setSkills([]));
    }
  }, [query === ""]); // eslint-disable-line react-hooks/exhaustive-deps
  const matches = useMemo(() => (skills ? filterSkills(skills, query ?? "") : []), [skills, query]);
  useEffect(() => setActive(0), [query]);
  const pick = (s: SkillInfo) => {
    setText(skillText(s));
    setDismissed(false);
    ref.current?.focus();
  };

  // 等权限确认时输入框是 disabled 的，浏览器会因此让它失去焦点。
  // 确认完、重新可用时把焦点还回来；只在焦点无处可去（落在 body 上）时才抢，不打断设置页等其他输入。
  useEffect(() => {
    const el = document.activeElement;
    if (!waiting && (!el || el === document.body)) ref.current?.focus();
  }, [waiting, running]);

  // 高度跟着内容变化：按行数算不准（长句自动换行也会占多行），所以按 scrollHeight 算。
  // 先清掉高度再量，内容变少时才能缩回去；上限由 CSS 的 max-h 控制，超出后出现滚动条。
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  }, [text]);

  const canSend = (text.trim() !== "" || pending.length > 0) && !waiting;
  const submit = () => {
    if (!canSend) return;
    const attachments = pending.map((p) => p.attachment);
    const msg = text.trim();
    setText("");
    setPending([]);
    setAttachError("");
    void send(msg, attachments);
  };

  // 添加文件：逐个读取和检查，不合格的给出原因，合格的加进待发送附件。
  const addFiles = async (files: File[]) => {
    if (files.length === 0) return;
    const errors: string[] = [];
    let current = pending;
    for (const f of files) {
      const r = await readAttachment(f);
      if (!r.ok) {
        errors.push(r.error);
        continue;
      }
      const err = checkAdd(current, r.item);
      if (err) {
        errors.push(`${r.item.name}：${err}`);
        continue;
      }
      current = [...current, r.item];
    }
    setPending(current);
    setAttachError(errors.join("\n"));
  };
  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(e.clipboardData.files);
    if (files.length === 0) return; // 普通文字粘贴照常进行
    e.preventDefault();
    void addFiles(files);
  };
  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setDragging(false);
    void addFiles(Array.from(e.dataTransfer.files));
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // 技能菜单打开时，方向键 / Tab / Enter / Esc 归菜单；没有匹配项时不拦截，Enter 照常发送
    if (menuOpen && !e.nativeEvent.isComposing) {
      if (e.key === "Escape") {
        e.preventDefault();
        setDismissed(true);
        return;
      }
      if (matches.length > 0) {
        if (e.key === "ArrowDown" || e.key === "ArrowUp") {
          e.preventDefault();
          setActive((i) => (i + (e.key === "ArrowDown" ? 1 : matches.length - 1)) % matches.length);
          return;
        }
        if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey)) {
          e.preventDefault();
          pick(matches[active] ?? matches[0]);
          return;
        }
      }
    }
    if (isSendKey({ key: e.key, shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, metaKey: e.metaKey, isComposing: e.nativeEvent.isComposing }, sendKey)) {
      e.preventDefault();
      submit();
    }
  };

  const hint =
    sendKey === "enter" ? t("Enter 发送 · Shift+Enter 换行") : t("{key}+Enter 发送 · Enter 换行", { key: MOD_KEY });
  const placeholder = waiting
    ? t("等待你确认工具调用…")
    : running
      ? t("agent 正在工作…现在发送的消息会排队，这一轮结束后自动发出")
      : sendKey === "enter"
        ? t("输入消息，Enter 发送，Shift+Enter 换行")
        : t("输入消息，{key}+Enter 发送，Enter 换行", { key: MOD_KEY });

  return (
    <div className="shrink-0 px-6 pt-3">
      <div className="mx-auto max-w-[var(--content-width)] space-y-2">
        <QueuePanel />
        <div
          className={`relative rounded-card border bg-panel shadow-card transition-colors focus-within:border-accent focus-within:ring-2 focus-within:ring-[var(--focus-ring)] ${dragging ? "border-accent ring-2 ring-[var(--focus-ring)]" : "border-line"}`}
          onDragOver={(e) => {
            if (e.dataTransfer.types.includes("Files")) {
              e.preventDefault();
              setDragging(true);
            }
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={onDrop}
        >
          {menuOpen && skills && <SkillMenu matches={matches} total={skills.length} query={query ?? ""} active={active} onPick={pick} onHover={setActive} />}
          {dragging && <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-card bg-accent-soft/80 text-sm font-medium text-accent">{t("松开以添加附件")}</div>}
          {pending.length > 0 && (
            <ul className="flex flex-wrap gap-2 px-3 pt-3" aria-label={t("待发送的附件")}>
              {pending.map((p) => (
                <li key={p.id} className="group relative">
                  {p.kind === "image" ? (
                    <img src={p.preview} alt={p.name} title={`${p.name} · ${fmtSize(p.size)}`} className="h-14 w-14 rounded-ctl border border-line object-cover" />
                  ) : (
                    <div className="flex h-14 max-w-[14rem] items-center gap-2 rounded-ctl border border-line bg-panel-2 px-2.5" title={p.name}>
                      <span aria-hidden="true">📄</span>
                      <span className="min-w-0">
                        <span className="block truncate text-xs font-medium">{p.name}</span>
                        <span className="block text-[10px] text-muted">{fmtSize(p.size)}</span>
                      </span>
                    </div>
                  )}
                  <button
                    className="absolute -right-1.5 -top-1.5 flex h-5 w-5 items-center justify-center rounded-full border border-line bg-panel text-[10px] text-muted shadow-card hover:text-danger focus-visible:outline-2 focus-visible:outline-accent"
                    onClick={() => setPending(pending.filter((x) => x.id !== p.id))}
                    aria-label={t("移除附件 {name}", { name: p.name })}
                  >
                    ✕
                  </button>
                </li>
              ))}
            </ul>
          )}
          {attachError && (
            <div role="alert" className="mx-3 mt-2 flex items-start gap-2 rounded-ctl border border-warn/40 bg-warn/10 px-2.5 py-1.5 text-xs text-warn">
              <span className="min-w-0 flex-1 whitespace-pre-wrap break-words">{attachError}</span>
              <button className="shrink-0 opacity-70 hover:opacity-100" onClick={() => setAttachError("")} aria-label={t("关闭")}>
                ✕
              </button>
            </div>
          )}
          <textarea
            ref={ref}
            autoFocus
            className="block max-h-60 min-h-[28px] w-full resize-none overflow-y-auto bg-transparent px-4 pb-1 pt-3 text-[15px] leading-relaxed outline-none placeholder:text-muted disabled:cursor-not-allowed"
            rows={1}
            placeholder={placeholder}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKeyDown}
            onPaste={onPaste}
            disabled={waiting}
            aria-label={t("消息输入框")}
          />
          <div className="flex items-center gap-2 px-3 pb-2 pt-1">
            <input
              ref={fileRef}
              type="file"
              multiple
              accept={ACCEPT}
              className="hidden"
              onChange={(e) => {
                void addFiles(Array.from(e.target.files ?? []));
                e.target.value = ""; // 允许再次选择同一个文件
              }}
            />
            <button className="flex items-center gap-1 rounded-ctl px-2 py-1 text-xs text-muted hover:bg-panel-2 hover:text-fg focus-visible:outline-2 focus-visible:outline-accent" onClick={() => fileRef.current?.click()} title={t("添加附件：图片、Markdown / 纯文本文件（也可以粘贴或拖进来）")} aria-label={t("添加附件")}>
              <span aria-hidden="true">📎</span> {t("附件")}
            </button>
            <button className="rounded-ctl px-2 py-1 text-xs text-muted hover:bg-panel-2 hover:text-fg" onClick={() => void openSettings("permissions")} title={t("查看和管理 agent 可用的工具")}>
              🛠 {t("工具")}
            </button>
            <span className="hidden text-[11px] text-muted sm:inline">{hint}</span>
            <div className="flex-1" />
            {running && (
              <button
                className="flex items-center gap-1.5 rounded-ctl border border-danger/50 px-3 py-1.5 text-sm text-danger hover:bg-danger/10 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-danger"
                onClick={() => void cancel()}
                title={queued > 0 ? t("只停止当前这一轮；待发送的 {n} 条消息会保留", { n: queued }) : t("停止当前这一轮")}
              >
                <span className="inline-block h-2.5 w-2.5 rounded-[2px] bg-current" aria-hidden="true" />
                {t("停止")}
              </button>
            )}
            <button className="btn-primary px-4" onClick={submit} disabled={!canSend} title={running ? t("加入待发送队列：这一轮结束后自动发出") : undefined}>
              {running ? t("加入队列") : t("发送")} <span aria-hidden="true">↑</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

// 技能菜单：浮在输入框上方。没有任何技能时说明去哪里放；有技能但没有匹配时提示"没有匹配"。
function SkillMenu({ matches, total, query, active, onPick, onHover }: { matches: SkillInfo[]; total: number; query: string; active: number; onPick: (s: SkillInfo) => void; onHover: (i: number) => void }) {
  return (
    <div className="shadow-pop absolute bottom-full left-0 right-0 z-30 mb-2 max-h-72 overflow-y-auto rounded-card border border-line bg-panel p-1.5" role="listbox" aria-label={t("技能")}>
      {total === 0 ? (
        <div className="px-3 py-3 text-sm text-muted">
          <div className="font-medium text-fg">{t("还没有技能")}</div>
          <p className="mt-1 text-xs leading-relaxed">
            {t("技能是一个文件夹，里面放一个 SKILL.md（写明名字和什么时候用）。放到工作区的 .myllmapp/skills 或 .claude/skills，或者你的用户目录下的 ~/.myllmapp/skills、~/.claude/skills，这里就会列出来；输入 /技能名 开头的消息即可让模型使用它。")}
          </p>
        </div>
      ) : matches.length === 0 ? (
        <div className="px-3 py-2.5 text-sm text-muted">{t("没有匹配“{q}”的技能", { q: query })}</div>
      ) : (
        matches.map((s, i) => (
          <button
            key={s.name}
            role="option"
            aria-selected={i === active}
            className={`flex w-full items-start gap-3 rounded-ctl px-3 py-2 text-left ${i === active ? "bg-accent-soft" : "hover:bg-panel-2"}`}
            onMouseEnter={() => onHover(i)}
            onMouseDown={(e) => {
              e.preventDefault(); // 不要让输入框失去焦点
              onPick(s);
            }}
          >
            <span className="shrink-0 font-mono text-sm font-medium">/{s.name}</span>
            <span className="min-w-0 flex-1 truncate text-xs text-muted" title={s.description}>
              {s.description}
            </span>
            <span className="shrink-0 rounded-full border border-line px-1.5 text-[10px] text-muted" title={`${s.origin}\n${s.dir}`}>
              {s.source === "project" ? t("项目") : t("个人")}
            </span>
          </button>
        ))
      )}
    </div>
  );
}
