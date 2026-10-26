import { useCallback, useEffect, useState, type ReactNode } from "react";
import { backend, type PluginActionResult, type PluginConfigView, type PluginInfo, type PluginPreview, type PluginStatus } from "../bridge";
import { t } from "../i18n";
import PluginInstallDialog from "./PluginInstallDialog";

// 设置 → 插件：已安装的插件列表 + 详情（设置表单、状态、操作按钮、依赖、提供的内容）。
//
// 界面完全由插件清单“声明”出来（表单字段、状态检查、按钮），插件本身不能往界面里注入任何代码。

const errMsg = (e: unknown) => String(e instanceof Error ? e.message : e);

export default function PluginSettings() {
  const [plugins, setPlugins] = useState<PluginInfo[] | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<PluginPreview | null>(null);
  const [installing, setInstalling] = useState(false);
  const [installError, setInstallError] = useState("");

  const reload = useCallback(async () => {
    try {
      const list = await backend().listPlugins();
      setPlugins(list);
      setSelected((cur) => (cur && list.some((p) => p.id === cur) ? cur : (list[0]?.id ?? null)));
    } catch (e) {
      setError(errMsg(e));
    }
  }, []);
  useEffect(() => void reload(), [reload]);

  const startInstall = async (kind: "dir" | "zip") => {
    setError("");
    try {
      const path = kind === "dir" ? await backend().chooseDirectory("") : await backend().chooseFile(t("选择插件压缩包"), t("插件压缩包（*.zip）"), "*.zip");
      if (!path) return;
      setInstallError("");
      setPreview(await backend().previewPlugin(path));
    } catch (e) {
      setError(errMsg(e));
    }
  };
  const confirmInstall = async (approved: string[]) => {
    if (!preview) return;
    setInstalling(true);
    setInstallError("");
    try {
      const info = await backend().installPlugin(preview.token, approved);
      setPreview(null);
      await reload();
      setSelected(info.id);
    } catch (e) {
      setInstallError(errMsg(e));
    } finally {
      setInstalling(false);
    }
  };
  const cancelInstall = () => {
    if (preview) void backend().cancelPluginPreview(preview.token).catch(() => {});
    setPreview(null);
  };

  const current = plugins?.find((p) => p.id === selected);
  const replace = (p: PluginInfo) => setPlugins((list) => (list ? list.map((x) => (x.id === p.id ? p : x)) : list));

  return (
    <div className="flex min-h-0 flex-1">
      <aside className="flex w-64 shrink-0 flex-col border-r border-line">
        <div className="flex gap-2 border-b border-line p-3">
          <button className="btn-primary flex-1 px-2 text-xs" onClick={() => void startInstall("dir")} title={t("选择一个插件文件夹（里面有 plugin.json）")}>
            {t("安装文件夹…")}
          </button>
          <button className="btn flex-1 px-2 text-xs" onClick={() => void startInstall("zip")} title={t("选择一个 .zip 插件包")}>
            {t("安装 zip…")}
          </button>
        </div>
        <ul className="min-h-0 flex-1 overflow-y-auto p-2" aria-label={t("已安装的插件")}>
          {plugins === null && <li className="px-2 py-2 text-xs text-muted">{t("加载中…")}</li>}
          {plugins?.length === 0 && <li className="px-2 py-2 text-xs leading-relaxed text-muted">{t("还没有安装插件。插件可以提供技能、斜杠命令和外部工具（MCP）。点上面的按钮选择一个插件文件夹或 zip 包。")}</li>}
          {plugins?.map((p) => (
            <li key={p.id}>
              <button className={`flex w-full items-center gap-2 rounded-ctl px-2.5 py-2 text-left text-sm hover:bg-accent-soft ${p.id === selected ? "bg-accent-soft" : ""}`} onClick={() => setSelected(p.id)}>
                <span className={`h-2 w-2 shrink-0 rounded-full ${p.error ? "bg-danger" : p.warning ? "bg-warn" : p.enabled ? "bg-ok" : "bg-muted/50"}`} aria-hidden="true" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{p.name}</span>
                  <span className="block truncate text-[11px] text-muted">
                    v{p.version} · {p.error ? t("不可用") : p.enabled ? t("已启用") : t("已停用")}
                  </span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      </aside>

      <section className="min-w-0 flex-1 overflow-y-auto p-5">
        {error && <div className="mb-3 rounded-ctl border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">{error}</div>}
        {current ? (
          <PluginDetail key={current.id} plugin={current} onChange={replace} onRemoved={() => void reload()} />
        ) : (
          <div className="text-sm text-muted">{t("选择左边的插件查看详情，或安装一个新的。")}</div>
        )}
      </section>

      {preview && <PluginInstallDialog preview={preview} busy={installing} error={installError} onConfirm={(a) => void confirmInstall(a)} onCancel={cancelInstall} />}
    </div>
  );
}

const STATUS_DOT: Record<PluginStatus["state"], string> = { ok: "bg-ok", bad: "bg-danger", missing_dependency: "bg-warn", unknown: "bg-warn", none: "" };
const STATUS_TEXT: Record<PluginStatus["state"], string> = { ok: "状态正常", bad: "状态异常", missing_dependency: "缺少依赖", unknown: "无法判断状态", none: "" };

function PluginDetail({ plugin: p, onChange, onRemoved }: { plugin: PluginInfo; onChange: (p: PluginInfo) => void; onRemoved: () => void }) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const run = async <T,>(fn: () => Promise<T>): Promise<T | undefined> => {
    setError("");
    setBusy(true);
    try {
      return await fn();
    } catch (e) {
      setError(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  const toggle = async () => {
    const r = await run(() => backend().setPluginEnabled(p.id, !p.enabled));
    if (r) onChange(r);
  };
  const uninstall = async () => {
    if (!confirm(t("卸载插件「{name}」？", { name: p.name }))) return;
    const purge = confirm(t("同时清除它的设置、保存在钥匙串里的密钥和它自己的数据吗？\n\n确定：全部清除\n取消：保留，以后重新安装时还能沿用"));
    if ((await run(() => backend().uninstallPlugin(p.id, purge).then(() => true))) !== undefined) onRemoved();
  };

  return (
    <div className="mx-auto max-w-2xl space-y-5">
      <header className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <h3 className="text-lg font-semibold">
            {p.name} <span className="text-sm font-normal text-muted">v{p.version}</span>
          </h3>
          {p.description && <p className="mt-1 text-sm text-muted">{p.description}</p>}
          <p className="mt-1 break-all text-[11px] text-muted">{p.source ? t("来源：{src}", { src: p.source }) : ""}</p>
        </div>
        <label className="flex shrink-0 cursor-pointer items-center gap-2 text-sm">
          <input type="checkbox" checked={p.enabled} onChange={() => void toggle()} disabled={busy || !!p.error} />
          {p.enabled ? t("已启用") : t("已停用")}
        </label>
      </header>

      {p.error && <div className="rounded-ctl border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">{p.error}</div>}
      {p.warning && <div className="rounded-ctl border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">{p.warning}</div>}
      {error && <div className="rounded-ctl border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">{error}</div>}

      {!p.error && p.has_status && <StatusBlock plugin={p} />}
      {!p.error && p.config.length > 0 && <ConfigForm plugin={p} onChange={onChange} />}
      {!p.error && p.actions.length > 0 && <ActionsBlock plugin={p} />}

      <Block title={t("提供的内容")}>
        <div className="space-y-1 text-sm">
          <Row label={t("技能")}>{p.skills.length ? p.skills.join("、") : "—"}</Row>
          <Row label={t("斜杠命令")}>{p.commands.length ? p.commands.map((c) => "/" + c.name).join("  ") : "—"}</Row>
          <Row label={t("外部工具")}>
            {p.servers.length ? (
              <span className="space-y-0.5">
                {p.servers.map((s) => (
                  <span key={s.name} className="block">
                    <span className="font-mono text-xs">{s.name}</span>{" "}
                    <span className="text-xs text-muted">
                      · {s.approved ? t("按“{risk}”运行（你已确认）", { risk: s.effective_risk === "read" ? t("只读") : t("写入") }) : t("每次调用都会询问")}
                    </span>
                  </span>
                ))}
              </span>
            ) : (
              "—"
            )}
          </Row>
          {p.requires.length > 0 && (
            <Row label={t("依赖")}>
              {p.requires.map((r) => (
                <span key={r.command} className="mr-3 whitespace-nowrap">
                  <span className={r.found ? "text-ok" : "text-warn"}>{r.found ? "✓" : "✗"}</span> <span className="font-mono text-xs">{r.command}</span>
                </span>
              ))}
            </Row>
          )}
          {p.network.length > 0 && <Row label={t("声明访问")}>{p.network.join("、")}</Row>}
        </div>
      </Block>

      <div className="pt-1">
        <button className="btn text-danger" onClick={() => void uninstall()} disabled={busy}>
          {t("卸载…")}
        </button>
      </div>
    </div>
  );
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="card p-4">
      <h4 className="mb-2 text-sm font-semibold">{title}</h4>
      {children}
    </section>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex gap-3">
      <div className="w-20 shrink-0 text-xs text-muted">{label}</div>
      <div className="min-w-0 flex-1 break-words">{children}</div>
    </div>
  );
}

// ---- 状态 ----

/** 打开详情时检查一次，之后每 30 秒一次；保存配置后也会重新检查（用 plugin.config 变化触发）。 */
function StatusBlock({ plugin: p }: { plugin: PluginInfo }) {
  const [st, setSt] = useState<PluginStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const configKey = JSON.stringify(p.config.map((c) => [c.value, c.has_secret]));
  const check = useCallback(async () => {
    setLoading(true);
    try {
      setSt(await backend().getPluginStatus(p.id));
    } catch (e) {
      setSt({ state: "unknown", message: errMsg(e) });
    } finally {
      setLoading(false);
    }
  }, [p.id]);
  useEffect(() => {
    void check();
    const timer = setInterval(() => void check(), 30000);
    return () => clearInterval(timer);
  }, [check, configKey, p.enabled]);
  return (
    <Block title={t("状态")}>
      <div className="flex items-center gap-2 text-sm">
        {st && st.state !== "none" && <span className={`h-2 w-2 rounded-full ${STATUS_DOT[st.state]}`} aria-hidden="true" />}
        <span>{st ? t(STATUS_TEXT[st.state] || "状态未知") : t("检查中…")}</span>
        <div className="flex-1" />
        <button className="btn px-2 py-0.5 text-xs" onClick={() => void check()} disabled={loading}>
          {loading ? t("检查中…") : t("重新检查")}
        </button>
      </div>
      {st?.message && <pre className="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-ctl bg-code px-2.5 py-2 font-mono text-[11px]">{st.message}</pre>}
      {st?.state === "missing_dependency" && <div className="mt-1 text-xs text-muted">{t("先安装缺少的命令，装好后点“重新检查”。")}</div>}
    </Block>
  );
}

// ---- 设置表单 ----

function ConfigForm({ plugin: p, onChange }: { plugin: PluginInfo; onChange: (p: PluginInfo) => void }) {
  const initial = () => Object.fromEntries(p.config.filter((c) => c.type !== "secret").map((c) => [c.key, c.value]));
  const [values, setValues] = useState<Record<string, string>>(initial);
  const [secrets, setSecrets] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const dirty = p.config.some((c) => (c.type === "secret" ? (secrets[c.key] ?? "") !== "" : values[c.key] !== c.value));
  const save = async () => {
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      const payload: Record<string, string> = { ...values };
      for (const [k, v] of Object.entries(secrets)) if (v) payload[k] = v;
      const r = await backend().savePluginConfig(p.id, payload);
      onChange(r);
      setSecrets({});
      setSaved(true);
    } catch (e) {
      setError(errMsg(e));
    } finally {
      setBusy(false);
    }
  };
  const clearSecret = async (key: string) => {
    setBusy(true);
    try {
      onChange(await backend().clearPluginSecret(p.id, key));
    } catch (e) {
      setError(errMsg(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Block title={t("设置")}>
      <div className="space-y-3">
        {p.config.map((c) => (
          <Field key={c.key} field={c}>
            {c.type === "secret" ? (
              <div className="flex items-center gap-2">
                <input
                  type="password"
                  className="input flex-1"
                  autoComplete="off"
                  placeholder={c.has_secret ? t("已设置（留空表示不修改）") : t("尚未设置")}
                  value={secrets[c.key] ?? ""}
                  onChange={(e) => setSecrets({ ...secrets, [c.key]: e.target.value })}
                />
                {c.has_secret && (
                  <button className="btn px-2 text-xs" onClick={() => void clearSecret(c.key)} disabled={busy}>
                    {t("清除")}
                  </button>
                )}
              </div>
            ) : c.type === "bool" ? (
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={values[c.key] === "true"} onChange={(e) => setValues({ ...values, [c.key]: e.target.checked ? "true" : "false" })} />
                {values[c.key] === "true" ? t("开") : t("关")}
              </label>
            ) : c.type === "select" ? (
              <select className="input" value={values[c.key] ?? ""} onChange={(e) => setValues({ ...values, [c.key]: e.target.value })}>
                {c.options?.map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
            ) : c.type === "path" ? (
              <div className="flex gap-2">
                <input className="input flex-1" value={values[c.key] ?? ""} onChange={(e) => setValues({ ...values, [c.key]: e.target.value })} />
                <button className="btn px-2 text-xs" onClick={async () => { const d = await backend().chooseDirectory(values[c.key] ?? ""); if (d) setValues({ ...values, [c.key]: d }); }}>
                  {t("选择…")}
                </button>
              </div>
            ) : (
              <input className="input" value={values[c.key] ?? ""} onChange={(e) => setValues({ ...values, [c.key]: e.target.value })} />
            )}
          </Field>
        ))}
      </div>
      {error && <div className="mt-3 text-sm text-danger">{error}</div>}
      <div className="mt-3 flex items-center gap-3">
        <button className="btn-primary" onClick={() => void save()} disabled={busy || !dirty}>
          {busy ? t("保存中…") : t("保存设置")}
        </button>
        {saved && !dirty && <span className="text-xs text-ok">{t("已保存")}</span>}
      </div>
    </Block>
  );
}

function Field({ field: c, children }: { field: PluginConfigView; children: ReactNode }) {
  return (
    <label className="block">
      <div className="mb-1 text-xs font-medium">
        {c.label}
        {c.required && <span className="text-danger"> *</span>}
        {c.type === "secret" && <span className="ml-1.5 rounded-full bg-panel-2 px-1.5 text-[10px] font-normal text-muted">{t("密钥")}</span>}
      </div>
      {children}
      {c.help && <div className="mt-1 text-[11px] text-muted">{c.help}</div>}
    </label>
  );
}

// ---- 操作按钮 ----

function ActionsBlock({ plugin: p }: { plugin: PluginInfo }) {
  const [running, setRunning] = useState<string | null>(null);
  const [results, setResults] = useState<Record<string, PluginActionResult>>({});
  const [last, setLast] = useState<string | null>(null);
  const run = async (id: string) => {
    setRunning(id);
    setLast(id);
    try {
      const r = await backend().runPluginAction(p.id, id);
      setResults((x) => ({ ...x, [id]: r }));
    } catch (e) {
      setResults((x) => ({ ...x, [id]: { output: "", exit_code: -1, error: errMsg(e) } }));
    } finally {
      setRunning(null);
    }
  };
  const r = last ? results[last] : undefined;
  return (
    <Block title={t("操作")}>
      <div className="flex flex-wrap gap-2">
        {p.actions.map((a) => (
          <button key={a.id} className="btn" onClick={() => void run(a.id)} disabled={running !== null}>
            {running === a.id ? t("运行中…") : a.label}
          </button>
        ))}
      </div>
      {r && (
        <div className="mt-3">
          {r.error && <div className="mb-1 text-sm text-danger">{r.error}</div>}
          {(r.output || !r.error) && (
            <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-ctl bg-code px-2.5 py-2 font-mono text-[11px] leading-relaxed">
              <Linkified text={r.output || t("（没有输出）")} />
            </pre>
          )}
          {r.exit_code !== 0 && !r.error && <div className="mt-1 text-xs text-warn">{t("命令以退出码 {n} 结束", { n: r.exit_code })}</div>}
        </div>
      )}
    </Block>
  );
}

/** 输出里的 http(s) 链接可以点击（例如登录授权页），在系统浏览器里打开。 */
function Linkified({ text }: { text: string }) {
  const parts = text.split(/(https?:\/\/[^\s<>"']+)/g);
  return (
    <>
      {parts.map((part, i) =>
        /^https?:\/\//.test(part) ? (
          <button key={i} type="button" className="break-all text-left text-accent underline" onClick={() => void backend().openExternalURL(part)}>
            {part}
          </button>
        ) : (
          <span key={i}>{part}</span>
        ),
      )}
    </>
  );
}
