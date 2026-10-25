import { useEffect, useState } from "react";
import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";
import { Notice } from "./GeneralSettings";

// 设置 → 系统提示词。
//
// 两种模式：使用内置默认（会随工作区自动更新），或自定义。
// 切到自定义时用默认提示词作为起点，方便在它的基础上修改，而不是从空白开始。

export default function PromptSettings() {
  const { general, saveGeneral } = useSettings();
  const running = useChat((s) => s.running);
  const [custom, setCustom] = useState<boolean | null>(null);
  const [text, setText] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!general || custom !== null) return;
    setCustom(!!general.saved.system_prompt);
    setText(general.saved.system_prompt || general.default_system_prompt);
  }, [general, custom]);

  if (!general || custom === null) return <div className="p-6 text-sm text-muted">加载中…</div>;
  // 配置文件里写了 system_prompt 时，"默认"指的是配置文件里的那份。
  const fileDefault = general.defaults.system_prompt;
  const defaultText = fileDefault || general.default_system_prompt;

  const save = async () => {
    const ok = await saveGeneral({ ...general.saved, system_prompt: custom ? text.trim() : "" });
    setSaved(ok);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-0 flex-1 flex-col gap-3 px-6 py-5">
        {running && <Notice tone="warn">agent 正在运行，本轮结束后才能修改。</Notice>}
        <div className="flex items-center gap-1 self-start rounded-lg border border-line p-1 text-sm">
          {[
            [false, fileDefault ? "使用配置文件中的提示词" : "使用内置默认"],
            [true, "自定义"],
          ].map(([v, label]) => (
            <button
              key={String(v)}
              className={`rounded-md px-3 py-1 ${custom === v ? "bg-accent text-on-accent" : "hover:bg-panel-2"}`}
              onClick={() => {
                setCustom(v as boolean);
                setSaved(false);
                if (v && !text.trim()) setText(defaultText);
              }}
              disabled={running}
            >
              {label as string}
            </button>
          ))}
        </div>
        <p className="text-[11px] text-muted">
          {custom
            ? "系统提示词会放在每次请求的最前面，决定 agent 的行为方式。建议保留工作区和工具使用的说明。修改后从下一轮对话开始生效。"
            : fileDefault
              ? "当前使用 config.toml 中 system_prompt 的内容（只读预览）。"
              : "内置默认提示词会根据当前工作区自动生成（只读预览）。切换到“自定义”可以在它的基础上修改。"}
        </p>
        <textarea
          className="input min-h-0 flex-1 resize-none font-mono text-[13px] leading-relaxed"
          value={custom ? text : defaultText}
          readOnly={!custom}
          onChange={(e) => {
            setText(e.target.value);
            setSaved(false);
          }}
          disabled={running}
          spellCheck={false}
        />
        <div className="text-right text-[11px] text-muted">{(custom ? text : defaultText).length} 字</div>
      </div>
      <footer className="flex shrink-0 items-center gap-3 border-t border-line px-6 py-3">
        {saved && <span className="text-xs text-ok">✓ 已保存，从下一轮对话开始生效</span>}
        <div className="flex-1" />
        {custom && (
          <button className="btn" onClick={() => setText(defaultText)} disabled={running}>
            用默认内容重新开始
          </button>
        )}
        <button className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" onClick={save} disabled={running || (custom && !text.trim())}>
          保存
        </button>
      </footer>
    </div>
  );
}
