import { useEffect, useState, type ReactNode } from "react";
import { backend, type PermissionRule, type ToolInfo } from "../bridge";
import { t } from "../i18n";

// 设置 → 权限与工具。
//
// 上半部分是权限规则：比状态栏上的档位更细，例如"go test 直接放行""rm 一律拒绝"。
// 规则列表有明确的"保存"按钮（顺序有意义，改到一半不应该生效）；工具开关则是点了就生效。

const errMsg = (e: unknown) => String(e instanceof Error ? e.message : e);

const EXAMPLES: PermissionRule[] = [
  { tool: "shell", pattern: "go test*", action: "allow" },
  { tool: "shell", pattern: "rm *", action: "deny" },
  { tool: "write_file", pattern: "docs/*", action: "allow" },
  { tool: "*", pattern: "*.env", action: "deny" },
  { tool: "mcp__github__*", pattern: "", action: "ask" },
];

export default function PermissionsSettings() {
  return (
    <div className="flex-1 space-y-8 overflow-y-auto px-6 py-5">
      <RulesEditor />
      <ToolToggles />
    </div>
  );
}

function Section({ title, hint, children }: { title: string; hint: string; children: ReactNode }) {
  return (
    <section>
      <div className="text-sm font-semibold">{title}</div>
      <p className="mb-3 mt-1 text-[11px] leading-relaxed text-muted">{hint}</p>
      {children}
    </section>
  );
}

function RulesEditor() {
  const [rules, setRules] = useState<PermissionRule[] | null>(null);
  const [tools, setTools] = useState<string[]>([]);
  const [state, setState] = useState<{ saved?: boolean; error?: string }>({});

  useEffect(() => {
    void backend()
      .getPermissionRules()
      .then(setRules)
      .catch((e) => setState({ error: errMsg(e) }));
    void backend()
      .listTools()
      .then((ts) => setTools(ts.map((x) => x.name)))
      .catch(() => {});
  }, []);

  if (!rules) return <div className="text-sm text-muted">{t("加载中…")}</div>;
  const change = (next: PermissionRule[]) => {
    setRules(next);
    setState({});
  };
  const patch = (i: number, p: Partial<PermissionRule>) => change(rules.map((r, j) => (j === i ? { ...r, ...p } : r)));
  const move = (i: number, d: -1 | 1) => {
    const j = i + d;
    if (j < 0 || j >= rules.length) return;
    const next = [...rules];
    [next[i], next[j]] = [next[j], next[i]];
    change(next);
  };
  const save = async () => {
    try {
      await backend().setPermissionRules(rules);
      setState({ saved: true });
    } catch (e) {
      setState({ error: errMsg(e) });
    }
  };

  const tone: Record<PermissionRule["action"], string> = { allow: "text-ok", ask: "text-warn", deny: "text-danger" };

  return (
    <Section
      title={t("权限规则")}
      hint={t("规则优先于状态栏上的权限档位，从上到下第一条匹配的生效。工具名和匹配内容都支持 * 通配；匹配内容对 shell 是命令，对文件工具是路径，留空匹配任意调用。")}
    >
      <datalist id="tool-names">
        {["*", ...tools].map((n) => (
          <option key={n} value={n} />
        ))}
      </datalist>
      {rules.length === 0 ? (
        <div className="rounded-lg border border-dashed border-line px-3 py-4 text-center text-xs text-muted">{t("还没有规则。")}</div>
      ) : (
        <ul className="space-y-2">
          {rules.map((r, i) => (
            <li key={i} className="flex items-center gap-2">
              <span className="w-5 shrink-0 text-right font-mono text-[11px] text-muted">{i + 1}</span>
              <input className="input w-48 font-mono" list="tool-names" value={r.tool} placeholder={t("工具名，如 shell")} onChange={(e) => patch(i, { tool: e.target.value })} />
              <input className="input flex-1 font-mono" value={r.pattern} placeholder={t("匹配内容，如 go test*")} onChange={(e) => patch(i, { pattern: e.target.value })} />
              <select className={`input w-24 ${tone[r.action]}`} value={r.action} onChange={(e) => patch(i, { action: e.target.value as PermissionRule["action"] })}>
                <option value="allow">{t("允许")}</option>
                <option value="ask">{t("询问")}</option>
                <option value="deny">{t("拒绝")}</option>
              </select>
              <button className="btn px-2 py-0.5" onClick={() => move(i, -1)} disabled={i === 0} title={t("上移")}>
                ↑
              </button>
              <button className="btn px-2 py-0.5" onClick={() => move(i, 1)} disabled={i === rules.length - 1} title={t("下移")}>
                ↓
              </button>
              <button className="px-1 text-muted hover:text-danger" onClick={() => change(rules.filter((_, j) => j !== i))} title={t("删除")}>
                ✕
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-3 flex items-center gap-2">
        <button className="btn" onClick={() => change([...rules, { tool: "shell", pattern: "", action: "ask" }])}>
          {t("＋ 添加规则")}
        </button>
        <div className="flex-1" />
        {state.error ? <span className="text-xs text-danger">{state.error}</span> : state.saved && <span className="text-xs text-ok">{t("✓ 已保存，立即生效")}</span>}
        <button className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-on-accent hover:opacity-90" onClick={() => void save()}>
          {t("保存规则")}
        </button>
      </div>
      <div className="mt-3 rounded-lg border border-line bg-panel-2 px-3 py-2 text-[11px] text-muted">
        {t("示例：")}
        {EXAMPLES.map((ex, i) => (
          <button
            key={i}
            className="mx-1 my-0.5 rounded border border-line px-1.5 py-0.5 font-mono hover:border-accent hover:text-accent"
            onClick={() => change([...rules, ex])}
          >
            {ex.tool} {ex.pattern || "*"} → {ex.action}
          </button>
        ))}
      </div>
    </Section>
  );
}

const RISK_LABEL: Record<string, string> = { read: "只读", write: "写入", exec: "执行" };

function ToolToggles() {
  const [tools, setTools] = useState<ToolInfo[] | null>(null);
  const [error, setError] = useState("");

  const load = () =>
    backend()
      .listTools()
      .then(setTools)
      .catch((e) => setError(errMsg(e)));
  useEffect(() => void load(), []);

  if (!tools) return null;
  const toggle = async (name: string) => {
    const next = tools.map((x) => (x.name === name ? { ...x, disabled: !x.disabled } : x));
    setTools(next);
    try {
      await backend().setDisabledTools(next.filter((x) => x.disabled).map((x) => x.name));
      setError("");
    } catch (e) {
      setError(errMsg(e));
      void load();
    }
  };

  return (
    <Section title={t("工具开关")} hint={t("关掉的工具不会发给模型，也就不会被调用。MCP 工具连接成功后也会出现在这里。")}>
      {error && <div className="mb-2 text-xs text-danger">{error}</div>}
      <ul className="divide-y divide-line rounded-lg border border-line">
        {tools.map((x) => (
          <li key={x.name} className="flex items-center gap-3 px-3 py-2">
            <input type="checkbox" id={`tool-${x.name}`} className="h-4 w-4 accent-[var(--accent)]" checked={!x.disabled} onChange={() => void toggle(x.name)} />
            <label htmlFor={`tool-${x.name}`} className={`min-w-0 flex-1 cursor-pointer ${x.disabled ? "opacity-50" : ""}`}>
              <div className="font-mono text-[13px]">{x.name}</div>
              <div className="truncate text-[11px] text-muted" title={x.description}>
                {x.description}
              </div>
            </label>
            <span className={`shrink-0 text-[11px] ${x.risk === "read" ? "text-muted" : "text-warn"}`}>
              {t("风险：{risk}", { risk: RISK_LABEL[x.risk] ? t(RISK_LABEL[x.risk]) : x.risk })}
            </span>
          </li>
        ))}
      </ul>
    </Section>
  );
}
