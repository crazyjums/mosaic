import { useEffect, useState, type ReactNode } from "react";
import { backend, type General, type KeychainStatus } from "../bridge";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";

// 设置 → 常规：工作区、最大步数、输出上限、命令超时、会话标题、API Key 存储位置。
//
// 和状态栏设置"改了就存"不同，这里有明确的"保存"按钮：换工作区影响很大（agent 读写的目录变了），
// 应该由用户确认后再生效。输入框留空 = 使用默认值，占位文字显示默认值是多少。

type Form = { workspace: string; maxSteps: string; maxTokens: string; shellTimeout: string; historyTurns: string; titleMode: NonNullable<General["title_mode"]> };

const toForm = (g: General): Form => ({
  workspace: g.workspace ?? "",
  maxSteps: g.max_steps ? String(g.max_steps) : "",
  maxTokens: g.max_tokens ? String(g.max_tokens) : "",
  shellTimeout: g.shell_timeout_seconds ? String(g.shell_timeout_seconds) : "",
  historyTurns: g.history_turns ? String(g.history_turns) : "",
  titleMode: g.title_mode ?? "",
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
      history_turns: num(form.historyTurns),
      system_prompt: general.saved.system_prompt, // 系统提示词在另一个标签页编辑，这里原样保留
      title_mode: form.titleMode,
    };
    if ([g.max_steps, g.max_tokens, g.shell_timeout_seconds, g.history_turns].some((n) => !Number.isInteger(n) || (n as number) < 0)) {
      setLocalError("步数、输出上限、超时、加载轮数都需要是正整数（留空表示使用默认值）");
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

        <Row
          label={t("默认工作区")}
          hint={t("agent 读写文件、执行命令的根目录，工具不能访问它之外的文件。每个会话可以在状态栏上单独选择工作区（新会话沿用当前会话的）；没有单独选过的会话使用这里的默认工作区。")}
        >
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
            {t("默认工作区：")}<span className="font-mono">{general.effective.workspace}</span>
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

        <Row
          label={t("历史会话加载轮数")}
          hint={t("打开历史会话时先加载最近这么多轮对话（一轮 = 一次你的发言和模型的回复），滚到顶部或点“加载更早的对话”时每次再加载这么多轮。会话很长时调小可以打开得更快；只影响显示，不影响发给模型的上下文。")}
        >
          <NumberInput value={form.historyTurns} onChange={(v) => patch({ historyTurns: v })} placeholder={`默认 ${d.history_turns ?? 20}`} unit={t("轮")} disabled={running} />
        </Row>

        <Row label={t("会话标题")} hint={t("“由模型生成”会在第一轮对话结束后，用当前模型额外发一次很短的请求（几百个 token）。失败时保留截取的第一句。")}>
          <div className="inline-flex gap-1 rounded-lg border border-line p-1">
            {(
              [
                ["first_message", t("截取第一句")],
                ["model", t("由模型生成")],
              ] as const
            ).map(([v, label]) => {
              const active = (form.titleMode || "first_message") === v;
              return (
                <button
                  key={v}
                  className={`rounded-md px-3 py-1 text-sm ${active ? "bg-accent text-on-accent" : "hover:bg-panel-2"}`}
                  onClick={() => patch({ titleMode: v })}
                  disabled={running}
                >
                  {label}
                </button>
              );
            })}
          </div>
        </Row>

        <ProxyRow />

        <KeychainRow />
      </div>

      <footer className="flex shrink-0 items-center gap-3 border-t border-line px-6 py-3">
        {localError ? <span className="text-xs text-danger">{localError}</span> : saved && <span className="text-xs text-ok">✓ 已保存，从下一轮对话开始生效</span>}
        <div className="flex-1" />
        <button className="btn" onClick={() => patch(toForm({}))} disabled={running} title="清空所有输入框，保存后全部使用默认值">
          全部恢复默认
        </button>
        <button className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" onClick={save} disabled={running}>
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

// 钥匙串开关与其他设置不同：点了立即生效（会马上迁移已有的密钥），不跟随下面的"保存"按钮。
function KeychainRow() {
  const [st, setSt] = useState<KeychainStatus | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    void backend()
      .getKeychain()
      .then(setSt)
      .catch((e) => setError(String(e instanceof Error ? e.message : e)));
  }, []);
  if (!st) return null;

  const toggle = async () => {
    try {
      setSt(await backend().setUseKeychain(!st.enabled));
      setError("");
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    }
  };
  // 已开启时即使钥匙串暂时不可用也允许关闭（把密钥迁回文件）。
  const canToggle = st.available || st.enabled;

  return (
    <Row
      label={t("API Key 保存到系统钥匙串")}
      hint={t("开启后，厂商的 API Key 保存在 macOS 钥匙串 / Windows 凭据管理器 / Linux Secret Service 中，settings.json 里只留引用。立即生效，已有的密钥会被迁移。")}
    >
      <label className={`flex items-center gap-2 text-sm ${canToggle ? "cursor-pointer" : "opacity-60"}`}>
        <input type="checkbox" className="h-4 w-4 accent-[var(--accent)]" checked={st.enabled} disabled={!canToggle} onChange={() => void toggle()} />
        {st.enabled ? t("已开启") : t("已关闭")}
      </label>
      {!st.available && st.error && <div className="mt-1 text-[11px] text-warn">{t("不可用：{error}", { error: st.error })}</div>}
      {st.warning && <div className="mt-1 text-[11px] text-warn">{st.warning}</div>}
      {error && <div className="mt-1 text-[11px] text-danger">{error}</div>}
    </Row>
  );
}

// 全局默认代理：没有单独设置代理的厂商都用它。和钥匙串开关一样，点"保存"立即生效，不跟随页面底部的保存按钮。
// 地址里的密码不会回传到界面（显示成 ***）；原样保存脱敏的地址表示不修改。
function ProxyRow() {
  const [value, setValue] = useState<string | null>(null);
  const [orig, setOrig] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  useEffect(() => {
    void backend()
      .getProxy()
      .then((p) => {
        setValue(p.display);
        setOrig(p.display);
      })
      .catch(() => setValue(""));
  }, []);
  if (value === null) return null;

  const save = async () => {
    try {
      await backend().setProxy(value);
      const p = await backend().getProxy();
      setValue(p.display);
      setOrig(p.display);
      setMsg({ ok: true, text: value.trim() ? t("已保存，立即生效") : t("已清除，使用环境变量或直连") });
    } catch (e) {
      setMsg({ ok: false, text: String(e instanceof Error ? e.message : e) });
    }
  };
  return (
    <Row label={t("网络代理（全局默认）")} hint={t("访问海外厂商时用。留空则沿用环境变量 HTTP_PROXY / HTTPS_PROXY（从开始菜单启动的应用通常读不到它们）。单个厂商可以在它自己的“高级选项”里改成别的代理，或填 direct 强制直连。")}>
      <div className="flex gap-2">
        <input
          className="input flex-1 font-mono"
          value={value}
          onChange={(e) => {
            setValue(e.target.value);
            setMsg(null);
          }}
          placeholder="http://127.0.0.1:7890  或  socks5://127.0.0.1:1080"
          spellCheck={false}
          autoComplete="off"
          aria-label={t("全局代理地址")}
        />
        <button className="btn" onClick={() => void save()} disabled={value === orig}>
          {t("保存")}
        </button>
      </div>
      {msg && <div className={`mt-1 text-xs ${msg.ok ? "text-ok" : "text-danger"}`}>{msg.text}</div>}
      <div className="mt-1 text-[11px] text-muted">{t("代理可以看到你的 API Key 和请求的目标地址。地址里带的密码保存在本机的设置文件里，和 API Key 一样。")}</div>
    </Row>
  );
}
