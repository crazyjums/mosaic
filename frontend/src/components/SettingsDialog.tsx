import { useEffect, useMemo, useState, type KeyboardEvent, type ReactNode } from "react";
import type { ModelMeta, ProviderInput, ProviderView, TestResult } from "../bridge";
import { PRESETS, type Preset } from "../store/presets";
import { useSettings } from "../store/settingsStore";
import { useChat } from "../store/chatStore";
import { t } from "../i18n";
import AboutSettings from "./AboutSettings";
import AppearanceSettings from "./AppearanceSettings";
import ChatGPTAccount from "./ChatGPTAccount";
import DataSettings from "./DataSettings";
import GeneralSettings from "./GeneralSettings";
import MCPSettings from "./MCPSettings";
import PermissionsSettings from "./PermissionsSettings";
import PromptSettings from "./PromptSettings";
import StatusLineSettings from "./StatusLineSettings";

// 标签页。label 是中文原文，渲染时翻译。
const TABS = [
  ["general", "常规"],
  ["providers", "模型厂商"],
  ["prompt", "系统提示词"],
  ["permissions", "权限与工具"],
  ["mcp", "MCP"],
  ["appearance", "外观"],
  ["statusline", "状态栏"],
  ["data", "数据"],
  ["about", "关于"],
] as const;

// 设置：模型厂商。左侧是配置档列表，右侧是编辑表单。
// 保存后立即生效（Go 端会热替换厂商目录），不需要重启应用。

const SOURCE_LABEL: Record<ProviderView["source"], string> = {
  config: "配置文件",
  settings: "",
  override: "已覆盖配置文件",
  account: "",
};

const TYPE_LABEL: Record<string, string> = { openai: "OpenAI 兼容", anthropic: "Anthropic", fake: "演示", chatgpt: "ChatGPT 账号" };

/** 左侧列表里"ChatGPT 账号登录"入口的选中值（还没登录时没有 chatgpt 配置档可选） */
const CHATGPT_ENTRY = "\u0000chatgpt";

export default function SettingsDialog() {
  const { open, tab, setTab, providers, error, hide } = useSettings();
  const running = useChat((s) => s.running);
  // selected：正在编辑的配置档名称；null 表示"新建"
  const [selected, setSelected] = useState<string | null>(null);

  // 打开时默认选中第一个"已经可用"的真实配置档；一个都还没配好时直接进入新建，
  // 而不是停在内置的、还没填密钥的 anthropic / openai 条目上。
  useEffect(() => {
    if (!open) return;
    const first = providers.find((p) => p.type !== "fake" && p.available);
    setSelected((cur) => (cur === CHATGPT_ENTRY || (cur && providers.some((p) => p.name === cur)) ? cur : first?.name ?? null));
  }, [open, providers]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: globalThis.KeyboardEvent) => e.key === "Escape" && hide();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, hide]);

  if (!open) return null;
  const current = providers.find((p) => p.name === selected) ?? null;

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-6" onMouseDown={hide}>
      <div
        className="flex h-[min(760px,92vh)] w-full max-w-5xl flex-col overflow-hidden rounded-xl border border-line bg-panel shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex h-11 shrink-0 items-center gap-1 overflow-x-auto border-b border-line px-3">
          {TABS.map(([id, label]) => (
            <button
              key={id}
              className={`shrink-0 rounded-md px-3 py-1 text-sm ${tab === id ? "bg-panel-2 font-medium text-fg" : "text-muted hover:text-fg"}`}
              onClick={() => setTab(id)}
            >
              {id === "mcp" ? label : t(label)}
            </button>
          ))}
          <div className="flex-1" />
          <button className="px-2 text-muted hover:text-fg" onClick={hide} title={t("关闭（Esc）")}>
            ✕
          </button>
        </div>
        {/* 运行中也能打开设置：外观、状态栏等立即生效；改厂商、工作区、最大步数这类要等这一轮结束才能保存 */}
        {running && (
          <div className="border-b border-warn/40 bg-warn/10 px-6 py-1.5 text-xs text-warn">
            {t("agent 正在运行：外观等设置可以照常修改；厂商、工作区、最大步数等运行参数要等这一轮结束（或点“停止”）后才能保存。")}
          </div>
        )}
        {/* 厂商页在自己的表单区域显示错误；其他页的错误显示在这里 */}
        {error && tab !== "providers" && <div className="border-b border-danger/40 bg-danger/10 px-6 py-2 text-sm text-danger">{error}</div>}
        {tab === "general" ? (
          <GeneralSettings />
        ) : tab === "prompt" ? (
          <PromptSettings />
        ) : tab === "about" ? (
          <AboutSettings />
        ) : tab === "data" ? (
          <DataSettings />
        ) : tab === "statusline" ? (
          <StatusLineSettings />
        ) : tab === "appearance" ? (
          <AppearanceSettings />
        ) : tab === "permissions" ? (
          <PermissionsSettings />
        ) : tab === "mcp" ? (
          <MCPSettings />
        ) : (
      <div className="flex min-h-0 flex-1">
        {/* 左：配置档列表 */}
        <aside className="flex w-60 shrink-0 flex-col border-r border-line">
          <div className="px-4 pb-2 pt-4 text-sm font-semibold">模型厂商</div>
          <nav className="flex-1 overflow-y-auto px-2">
            {providers.map((p) => (
              <button
                key={p.name}
                className={`flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm ${p.name === selected ? "bg-panel-2" : "hover:bg-panel-2/60"}`}
                onClick={() => setSelected(p.name)}
              >
                <span className={`h-2 w-2 shrink-0 rounded-full ${p.available ? "bg-ok" : "bg-muted/50"}`} title={p.available ? "可用" : p.error} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{p.name}</span>
                  <span className="block truncate text-[11px] text-muted">
                    {TYPE_LABEL[p.type] ?? p.type}
                    {SOURCE_LABEL[p.source] && ` · ${SOURCE_LABEL[p.source]}`}
                  </span>
                </span>
              </button>
            ))}
          </nav>
          <div className="space-y-2 border-t border-line p-3">
            {!providers.some((p) => p.type === "chatgpt") && (
              <button
                className={`w-full rounded-lg border px-3 py-2 text-sm ${selected === CHATGPT_ENTRY ? "border-accent text-accent" : "border-line hover:bg-panel-2"}`}
                onClick={() => setSelected(CHATGPT_ENTRY)}
              >
                {t("用 ChatGPT 账号登录")}
              </button>
            )}
            <button
              className={`w-full rounded-lg border border-dashed px-3 py-2 text-sm ${selected === null ? "border-accent text-accent" : "border-line hover:bg-panel-2"}`}
              onClick={() => setSelected(null)}
            >
              ＋ 添加厂商
            </button>
          </div>
        </aside>

        {/* 右：表单。key 保证切换配置档时表单状态重置 */}
        <section className="flex min-w-0 flex-1 flex-col">
          <header className="flex h-12 shrink-0 items-center border-b border-line px-5">
            <span className="text-sm font-medium">{selected === CHATGPT_ENTRY ? t("ChatGPT 账号") : current ? `编辑：${current.name}` : "添加厂商"}</span>
          </header>
          {error && <div className="border-b border-danger/40 bg-danger/10 px-5 py-2 text-sm text-danger">{error}</div>}
          {selected === CHATGPT_ENTRY ? (
            <div className="flex-1 overflow-y-auto px-5 py-4">
              <ChatGPTAccount onLoggedIn={() => setSelected("chatgpt")} />
            </div>
          ) : (
            <ProviderForm key={current?.name ?? "__new__"} current={current} onSaved={setSelected} />
          )}
        </section>
      </div>
        )}
      </div>
    </div>
  );
}

interface FormState {
  name: string;
  type: string;
  baseURL: string;
  apiKey: string;
  models: string[];
  defaultModel: string;
  options: Record<string, unknown>;
}

function fromView(v: ProviderView): FormState {
  return {
    name: v.name,
    type: v.type,
    baseURL: v.base_url,
    apiKey: "",
    models: v.models ?? [],
    defaultModel: v.default_model,
    options: { ...(v.options ?? {}) },
  };
}

function fromPreset(p: Preset): FormState {
  return { name: p.name, type: p.type, baseURL: p.baseURL, apiKey: "", models: [...p.models], defaultModel: p.models[0] ?? "", options: { ...(p.options ?? {}) } };
}

function ProviderForm({ current, onSaved }: { current: ProviderView | null; onSaved: (name: string) => void }) {
  const { save, remove, test, fetchModels } = useSettings();
  const [presetId, setPresetId] = useState("custom");
  const [form, setForm] = useState<FormState>(() => (current ? fromView(current) : fromPreset(PRESETS[0])));
  const [showKey, setShowKey] = useState(false);
  const [modelDraft, setModelDraft] = useState("");
  const [fetched, setFetched] = useState<string[] | null>(null);
  const [busy, setBusy] = useState<"" | "save" | "test" | "fetch">("");
  const [result, setResult] = useState<TestResult | null>(null);
  const [fetchError, setFetchError] = useState("");

  const isFake = current?.type === "fake";
  // ChatGPT 账号：没有 Base URL 和 API Key，名称固定；表单只编辑模型列表和思考选项。
  const isChatGPT = form.type === "chatgpt";
  const preset = PRESETS.find((p) => p.id === presetId);
  const patch = (p: Partial<FormState>) => setForm((f) => ({ ...f, ...p }));

  const input = (): ProviderInput => ({
    original_name: current?.name ?? "",
    name: form.name,
    type: form.type,
    base_url: form.baseURL,
    api_key: form.apiKey,
    models: form.models,
    default_model: form.models.includes(form.defaultModel) ? form.defaultModel : form.models[0] ?? "",
    options: form.options,
  });

  const addModels = (ids: string[]) => {
    const next = [...form.models];
    for (const id of ids.map((s) => s.trim()).filter(Boolean)) if (!next.includes(id)) next.push(id);
    patch({ models: next, defaultModel: form.defaultModel || next[0] || "" });
  };
  const removeModel = (id: string) => {
    const next = form.models.filter((m) => m !== id);
    patch({ models: next, defaultModel: form.defaultModel === id ? next[0] ?? "" : form.defaultModel });
  };
  const onModelKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if ((e.key === "Enter" || e.key === ",") && !e.nativeEvent.isComposing) {
      e.preventDefault();
      // 支持一次粘贴多个，用逗号或空格分隔
      addModels(modelDraft.split(/[,\s]+/));
      setModelDraft("");
    }
  };

  const doFetch = async () => {
    setBusy("fetch");
    setFetchError("");
    try {
      const list = await fetchModels(input());
      setFetched(list);
      if (list.length === 0) setFetchError("服务器返回了空列表，请手动填写模型名。");
    } catch (e) {
      setFetchError(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy("");
    }
  };

  const doTest = async () => {
    setBusy("test");
    setResult(null);
    setResult(await test(input()));
    setBusy("");
  };

  const doSave = async () => {
    setBusy("save");
    const ok = await save(input());
    setBusy("");
    if (ok) onSaved(form.name.trim());
  };

  const doRemove = async () => {
    if (!current) return;
    const text = current.source === "override" ? `恢复「${current.name}」为配置文件中的设置？` : `删除「${current.name}」？`;
    if (confirm(text)) await remove(current.name);
  };

  // 从服务器获取到的、还没加入列表的模型
  const candidates = useMemo(() => (fetched ?? []).filter((m) => !form.models.includes(m)), [fetched, form.models]);

  if (isFake) {
    return (
      <div className="p-6 text-sm text-muted">
        <p>
          <strong className="text-fg">fake</strong> 是内置的演示厂商：按固定剧本回复，用于离线体验和测试，不会调用任何真实模型。
        </p>
        <p className="mt-2">点击左下角“添加厂商”配置一个真实模型，保存后会自动切换过去。</p>
      </div>
    );
  }

  return (
    <>
      <div className="flex-1 space-y-5 overflow-y-auto px-5 py-4">
        {!current && (
          <Field label="快速选择">
            <select
              className="input"
              value={presetId}
              onChange={(e) => {
                const p = PRESETS.find((x) => x.id === e.target.value)!;
                setPresetId(p.id);
                setForm(fromPreset(p));
                setFetched(null);
                setResult(null);
              }}
            >
              {PRESETS.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label}
                </option>
              ))}
            </select>
            {preset?.hint && <Hint>{preset.hint}</Hint>}
          </Field>
        )}

        {current?.source === "config" && (
          <div className="rounded-lg border border-line bg-panel-2 px-3 py-2 text-xs text-muted">
            这个厂商来自配置文件（config.toml 或内置默认配置）。在这里保存会生成一份覆盖设置，原配置文件不会被修改。
          </div>
        )}
        {current && !current.available && current.error && (
          <div className="rounded-lg border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-warn">当前不可用：{current.error}</div>
        )}

        {isChatGPT && <ChatGPTAccount onLoggedIn={() => {}} />}

        {!isChatGPT && (
        <>
        <div className="grid grid-cols-2 gap-4">
          <Field label="名称">
            <input className="input" value={form.name} onChange={(e) => patch({ name: e.target.value })} placeholder="例如 deepseek、公司网关" />
          </Field>
          <Field label="协议">
            <div className="flex gap-1 rounded-lg border border-line p-1">
              {(["openai", "anthropic"] as const).map((t) => (
                <button
                  key={t}
                  className={`flex-1 rounded-md px-2 py-1 text-sm ${form.type === t ? "bg-accent text-on-accent" : "hover:bg-panel-2"}`}
                  onClick={() => patch({ type: t })}
                >
                  {TYPE_LABEL[t]}
                </button>
              ))}
            </div>
          </Field>
        </div>

        <Field label="Base URL">
          <input
            className="input font-mono"
            value={form.baseURL}
            onChange={(e) => patch({ baseURL: e.target.value })}
            placeholder={form.type === "anthropic" ? "https://api.anthropic.com（留空使用官方地址）" : "https://api.example.com/v1"}
          />
          <Hint>
            {form.type === "anthropic"
              ? "不含 /v1/messages。使用官方服务可以留空。"
              : "通常以 /v1 结尾，程序会在后面拼上 /chat/completions。"}
          </Hint>
        </Field>

        <Field label="API Key">
          <div className="flex gap-2">
            <input
              className="input flex-1 font-mono"
              type={showKey ? "text" : "password"}
              value={form.apiKey}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => patch({ apiKey: e.target.value })}
              placeholder={current?.has_api_key ? `已保存 ${current.api_key_hint}（留空则不修改）` : form.type === "openai" ? "sk-...（本地服务可留空）" : "sk-ant-..."}
            />
            <button className="btn" onClick={() => setShowKey(!showKey)}>
              {showKey ? "隐藏" : "显示"}
            </button>
          </div>
          <Hint>密钥只保存在本机（数据目录下的 settings.json；在“常规”中开启后改为保存到系统钥匙串），不会显示在界面上。</Hint>
        </Field>
        </>
        )}

        <Field label="模型列表">
          <div className="flex min-h-[42px] flex-wrap items-center gap-1.5 rounded-lg border border-line bg-bg px-2 py-1.5 focus-within:border-accent/60">
            {form.models.map((m) => (
              <span key={m} className="flex items-center gap-1 rounded-md bg-panel-2 px-2 py-0.5 font-mono text-xs">
                {m}
                <button className="text-muted hover:text-danger" onClick={() => removeModel(m)} title="移除">
                  ×
                </button>
              </span>
            ))}
            <input
              className="min-w-[160px] flex-1 bg-transparent px-1 py-0.5 font-mono text-sm outline-none placeholder:text-muted"
              value={modelDraft}
              onChange={(e) => setModelDraft(e.target.value)}
              onKeyDown={onModelKey}
              onBlur={() => {
                if (modelDraft.trim()) {
                  addModels(modelDraft.split(/[,\s]+/));
                  setModelDraft("");
                }
              }}
              placeholder={form.models.length ? "继续输入，回车添加" : "输入模型名，回车添加"}
            />
          </div>
          <div className="mt-2 flex items-center gap-2">
            <button className="btn" onClick={doFetch} disabled={busy !== ""}>
              {busy === "fetch" ? "获取中…" : "从服务器获取模型列表"}
            </button>
            {fetchError && <span className="text-xs text-danger">{fetchError}</span>}
          </div>
          {candidates.length > 0 && (
            <div className="mt-2 rounded-lg border border-line p-2">
              <div className="mb-1.5 flex items-center justify-between text-xs text-muted">
                <span>服务器上有 {fetched!.length} 个模型，点击添加：</span>
                <button className="text-accent hover:underline" onClick={() => addModels(candidates)}>
                  全部添加
                </button>
              </div>
              <div className="flex max-h-40 flex-wrap gap-1.5 overflow-y-auto">
                {candidates.map((m) => (
                  <button key={m} className="rounded-md border border-line px-2 py-0.5 font-mono text-xs hover:border-accent hover:text-accent" onClick={() => addModels([m])}>
                    ＋ {m}
                  </button>
                ))}
              </div>
            </div>
          )}
        </Field>

        {form.models.length > 0 && (
          <Field label="默认模型">
            <select className="input font-mono" value={form.models.includes(form.defaultModel) ? form.defaultModel : form.models[0]} onChange={(e) => patch({ defaultModel: e.target.value })}>
              {form.models.map((m) => (
                <option key={m}>{m}</option>
              ))}
            </select>
          </Field>
        )}

        <details className="rounded-lg border border-line px-3 py-2">
          <summary className="cursor-pointer text-sm text-muted">高级选项</summary>
          <div className="mt-3 space-y-4">
            {isChatGPT ? (
              <>
                <Field label="思考过程">
                  <select
                    className="input"
                    value={String(form.options.thinking ?? "adaptive")}
                    onChange={(e) => patch({ options: { ...form.options, thinking: e.target.value } })}
                  >
                    <option value="adaptive">请求思考摘要（界面显示在思考卡片里）</option>
                    <option value="omit">不发送 reasoning 参数（模型不支持时选这个）</option>
                  </select>
                </Field>
                <Field label="Effort（思考深度）">
                  <select
                    className="input"
                    value={String(form.options.effort ?? "")}
                    onChange={(e) => patch({ options: { ...form.options, effort: e.target.value || undefined } })}
                  >
                    <option value="">模型默认</option>
                    {["minimal", "low", "medium", "high", "xhigh"].map((x) => (
                      <option key={x}>{x}</option>
                    ))}
                  </select>
                </Field>
              </>
            ) : form.type === "openai" ? (
              <Field label="输出上限字段名">
                <select
                  className="input"
                  value={String(form.options.max_tokens_field ?? "max_tokens")}
                  onChange={(e) => patch({ options: { ...form.options, max_tokens_field: e.target.value } })}
                >
                  <option value="max_tokens">max_tokens（大多数兼容服务）</option>
                  <option value="max_completion_tokens">max_completion_tokens（OpenAI 新模型）</option>
                </select>
              </Field>
            ) : (
              <>
                <Field label="思考过程">
                  <select
                    className="input"
                    value={String(form.options.thinking ?? "adaptive")}
                    onChange={(e) => patch({ options: { ...form.options, thinking: e.target.value } })}
                  >
                    <option value="adaptive">开启（自适应思考，界面显示思考摘要）</option>
                    <option value="omit">不发送 thinking 参数（旧模型或代理不支持时选这个）</option>
                  </select>
                </Field>
                <Field label="Effort（思考深度）">
                  <select
                    className="input"
                    value={String(form.options.effort ?? "")}
                    onChange={(e) => patch({ options: { ...form.options, effort: e.target.value || undefined } })}
                  >
                    <option value="">模型默认</option>
                    {["low", "medium", "high", "xhigh", "max"].map((x) => (
                      <option key={x}>{x}</option>
                    ))}
                  </select>
                </Field>
                <Field label="拒绝回退">
                  <label className="flex cursor-pointer items-start gap-2 text-sm">
                    <input
                      type="checkbox"
                      className="mt-0.5 accent-[var(--accent)]"
                      checked={form.options.refusal_fallback === true}
                      onChange={(e) => patch({ options: { ...form.options, refusal_fallback: e.target.checked || undefined } })}
                    />
                    <span>
                      请求被安全策略拒绝时，由服务端自动换用合适的模型重试
                      <span className="block text-[11px] text-muted">会在请求中加上 fallbacks="default" 和对应的 beta 头；使用不支持该功能的代理时请关闭。</span>
                    </span>
                  </label>
                </Field>
              </>
            )}
            {form.models.length > 0 && (
              <Field label="模型信息（用于状态栏显示上下文占用和预估花费，可留空）">
                <ModelMetaTable models={form.models} meta={metaOf(form.options)} onChange={(m) => patch({ options: { ...form.options, models_meta: m } })} />
              </Field>
            )}
          </div>
        </details>

      </div>

        {result && (
        <div className={`shrink-0 border-t px-5 py-2 text-sm ${result.ok ? "border-ok/40 bg-ok/10 text-ok" : "border-danger/40 bg-danger/10 text-danger"}`}>
          {result.ok ? (
            <>
              ✓ 连接成功 · {result.model} · {result.latency_ms} ms{result.reply && <span className="text-muted"> · 回复：{result.reply}</span>}
            </>
          ) : (
            <>✗ {result.error}</>
          )}
        </div>
      )}
      <footer className="flex shrink-0 items-center gap-2 border-t border-line px-5 py-3">
        {current?.deletable && (
          <button className="rounded-md px-3 py-1.5 text-sm text-danger hover:bg-danger/10" onClick={doRemove} disabled={busy !== ""}>
            {current.source === "override" ? "恢复配置文件设置" : "删除"}
          </button>
        )}
        <div className="flex-1" />
        <button className="btn" onClick={doTest} disabled={busy !== "" || form.models.length === 0} title="用默认模型发一条极短的消息，会产生极少量费用">
          {busy === "test" ? "测试中…" : "测试连接"}
        </button>
        <button className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" onClick={doSave} disabled={busy !== ""}>
          {busy === "save" ? "保存中…" : "保存"}
        </button>
      </footer>
    </>
  );
}

type MetaMap = Record<string, ModelMeta>;

function metaOf(options: Record<string, unknown>): MetaMap {
  const m = options.models_meta;
  return m && typeof m === "object" ? (m as MetaMap) : {};
}

// 每个模型一行：上下文窗口、输入单价、输出单价。空着表示不知道，状态栏就不显示对应的比例或花费。
function ModelMetaTable({ models, meta, onChange }: { models: string[]; meta: MetaMap; onChange: (m: MetaMap) => void }) {
  const set = (model: string, key: keyof ModelMeta, raw: string) => {
    const next: MetaMap = { ...meta, [model]: { ...meta[model] } };
    const v = parseFloat(raw);
    if (raw.trim() === "" || !Number.isFinite(v) || v <= 0) delete next[model][key];
    else next[model][key] = key === "context_window" ? Math.round(v) : v;
    if (Object.keys(next[model]).length === 0) delete next[model];
    onChange(next);
  };
  const cell = "w-full rounded border border-line bg-bg px-2 py-1 text-right font-mono text-xs outline-none focus:border-accent/60";
  return (
    <table className="w-full text-xs">
      <thead className="text-muted">
        <tr>
          <th className="pb-1 text-left font-normal">模型</th>
          <th className="w-32 pb-1 text-right font-normal">上下文窗口（tokens）</th>
          <th className="w-28 pb-1 text-right font-normal">输入 $/百万</th>
          <th className="w-28 pb-1 text-right font-normal">输出 $/百万</th>
        </tr>
      </thead>
      <tbody>
        {models.map((m) => (
          <tr key={m}>
            <td className="truncate py-0.5 pr-2 font-mono">{m}</td>
            {(["context_window", "input_price", "output_price"] as const).map((k) => (
              <td key={k} className="py-0.5 pl-2">
                <input className={cell} inputMode="decimal" placeholder="—" defaultValue={meta[m]?.[k] ?? ""} onBlur={(e) => set(m, k, e.target.value)} />
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// 用 div 而不是 label：label 被点击时会激活它里面的第一个按钮（例如模型标签上的 ×）。
function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div className="mb-1.5 text-xs font-medium text-muted">{label}</div>
      {children}
    </div>
  );
}

function Hint({ children }: { children: ReactNode }) {
  return <span className="mt-1 block text-[11px] text-muted">{children}</span>;
}
