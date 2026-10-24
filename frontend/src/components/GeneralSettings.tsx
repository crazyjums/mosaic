import { useEffect, useState, type ReactNode } from "react";
import { backend, type General } from "../bridge";
import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";

// 设置 → 常规：工作区、最大步数、输出上限、命令超时。
//
// 和状态栏设置"改了就存"不同，这里有明确的"保存"按钮：换工作区影响很大（agent 读写的目录变了），
// 应该由用户确认后再生效。输入框留空 = 使用默认值，占位文字显示默认值是多少。

type Form = { workspace: string; maxSteps: string; maxTokens: string; shellTimeout: string };

const toForm = (g: General): Form => ({
  workspace: g.workspace ?? "",
  maxSteps: g.max_steps ? String(g.max_steps) : "",
  maxTokens: g.max_tokens ? String(g.max_tokens) : "",
  shellTimeout: g.shell_timeout_seconds ? String(g.shell_timeout_seconds) : "",
});

/** "" → 0（使用默认）；非法数字返回 NaN，交给保存前的校验 */
const num = (s: string) => (s.trim() === "" ? 0 : Number(s));

export default function GeneralSettings() {
  const { general, saveGeneral } = useSettings();
  const running = useChat((s) => s.running);
  const [form, setForm] = useState<Form | null>(null);
  const [saved, setSaved] = useState(false);
  const [localError, setLocalError] = useState("");

  useEffect(() => {
    if (general && form === null) setForm(toForm(general.saved));
  }, [general, form]);

  if (!general || !form) return <div className="p-6 text-sm text-muted">加载中…</div>;
  const d = general.defaults;
  const patch = (p: Partial<Form>) => {
    setForm({ ...form, ...p });
    setSaved(false);
  };

  const choose = async () => {
    const dir = await backend().chooseDirectory(form.workspace || general.effective.workspace || "");
    if (dir) patch({ workspace: dir });
  };

  const save = async () => {
    const g: General = {
      workspace: form.workspace.trim(),
      max_steps: num(form.maxSteps),
      max_tokens: num(form.maxTokens),
      shell_timeout_seconds: num(form.shellTimeout),
      system_prompt: general.saved.system_prompt, // 系统提示词在另一个标签页编辑，这里原样保留
    };
    if ([g.max_steps, g.max_tokens, g.shell_timeout_seconds].some((n) => !Number.isInteger(n) || (n as number) < 0)) {
      setLocalError("步数、输出上限、超时都需要是正整数（留空表示使用默认值）");
      return;
    }
    setLocalError("");
    if (await saveGeneral(g)) setSaved(true);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex-1 space-y-6 overflow-y-auto px-6 py-5">
        {running && <Notice tone="warn">agent 正在运行，本轮结束后才能修改这些设置。</Notice>}
        {general.workspace_warning && <Notice tone="warn">{general.workspace_warning}</Notice>}

        <Row label="工作区" hint="agent 读写文件、执行命令的根目录。工具不能访问这个目录之外的文件。">
          {general.workspace_locked ? (
            <div className="input bg-panel-2 font-mono text-muted">{general.effective.workspace}（由启动参数 -workspace 指定，不能在这里修改）</div>
          ) : (
            <div className="flex gap-2">
              <input
                className="input flex-1 font-mono"
                value={form.workspace}
                onChange={(e) => patch({ workspace: e.target.value })}
                placeholder={`默认：${d.workspace}`}
                disabled={running}
              />
              <button className="btn" onClick={choose} disabled={running}>
                选择文件夹…
              </button>
            </div>
          )}
          <div className="mt-1 text-[11px] text-muted">
            当前生效：<span className="font-mono">{general.effective.workspace}</span>。切换后，之前"本会话总是允许"的授权会被清除。
          </div>
        </Row>

        <Row label="最大步数" hint="一轮对话中最多调用模型的次数，防止 agent 陷入死循环反复调用工具。达到上限后可以发送“继续”。">
          <NumberInput value={form.maxSteps} onChange={(v) => patch({ maxSteps: v })} placeholder={`默认 ${d.max_steps}`} unit="步" disabled={running} />
        </Row>

        <Row label="单次输出上限" hint="每次模型调用最多输出的 token 数。留空时由各厂商适配器决定（Anthropic 为 64000，OpenAI 兼容服务不限制）。">
          <NumberInput
            value={form.maxTokens}
            onChange={(v) => patch({ maxTokens: v })}
            placeholder={d.max_tokens ? `默认 ${d.max_tokens}` : "默认由厂商决定"}
            unit="tokens"
            disabled={running}
          />
        </Row>

        <Row label="命令超时" hint="shell 工具执行单条命令的最长时间，超时会结束整个进程组。">
          <NumberInput value={form.shellTimeout} onChange={(v) => patch({ shellTimeout: v })} placeholder={`默认 ${d.shell_timeout_seconds}`} unit="秒" disabled={running} />
        </Row>
      </div>

      <footer className="flex shrink-0 items-center gap-3 border-t border-line px-6 py-3">
        {localError ? <span className="text-xs text-danger">{localError}</span> : saved && <span className="text-xs text-ok">✓ 已保存，从下一轮对话开始生效</span>}
        <div className="flex-1" />
        <button className="btn" onClick={() => patch(toForm({}))} disabled={running} title="清空所有输入框，保存后全部使用默认值">
          全部恢复默认
        </button>
        <button className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-white hover:opacity-90 disabled:opacity-50" onClick={save} disabled={running}>
          保存
        </button>
      </footer>
    </div>
  );
}

function Row({ label, hint, children }: { label: string; hint: string; children: ReactNode }) {
  return (
    <div>
      <div className="text-sm font-medium">{label}</div>
      <div className="mb-2 mt-0.5 text-[11px] text-muted">{hint}</div>
      {children}
    </div>
  );
}

function NumberInput(props: { value: string; onChange: (v: string) => void; placeholder: string; unit: string; disabled?: boolean }) {
  return (
    <div className="flex items-center gap-2">
      <input
        className="input w-48 font-mono"
        inputMode="numeric"
        value={props.value}
        onChange={(e) => props.onChange(e.target.value.replace(/[^\d]/g, ""))}
        placeholder={props.placeholder}
        disabled={props.disabled}
      />
      <span className="text-xs text-muted">{props.unit}</span>
    </div>
  );
}

export function Notice({ tone, children }: { tone: "warn" | "info"; children: ReactNode }) {
  const cls = tone === "warn" ? "border-warn/40 bg-warn/10 text-warn" : "border-line bg-panel-2 text-muted";
  return <div className={`rounded-lg border px-3 py-2 text-xs ${cls}`}>{children}</div>;
}
