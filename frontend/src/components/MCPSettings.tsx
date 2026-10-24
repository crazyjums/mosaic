import { useEffect, useState, type ReactNode } from "react";
import { backend, type MCPServerConfig, type MCPServerStatus } from "../bridge";
import { t } from "../i18n";
import { EMPTY_FORM, EXAMPLE_FORM, fromForm, toForm, type MCPForm } from "../store/mcpForm";
import { useChat } from "../store/chatStore";

// 设置 → MCP：添加 / 编辑 / 删除 MCP server，并显示连接状态。
//
// 左侧是 server 列表（带连接状态），右侧是编辑表单。保存时提交整个列表，
// 后端会比较新旧配置：只重连改动过的 server，没变的连接不受影响。

const errMsg = (e: unknown) => String(e instanceof Error ? e.message : e);

const DOT: Record<MCPServerStatus["state"], string> = {
  connecting: "bg-warn animate-pulse",
  connected: "bg-ok",
  failed: "bg-danger",
  disabled: "bg-muted/50",
};

export default function MCPSettings() {
  const [servers, setServers] = useState<MCPServerConfig[] | null>(null);
  const [statuses, setStatuses] = useState<MCPServerStatus[]>([]);
  // selected：正在编辑的下标；-1 表示新建
  const [selected, setSelected] = useState(-1);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const refreshMeta = useChat((s) => s.refreshMeta);

  useEffect(() => {
    backend()
      .listMCPServers()
      .then((list) => {
        setServers(list);
        setSelected(list.length ? 0 : -1);
      })
      .catch((e) => setError(errMsg(e)));
    // 连接在后台进行，页面打开期间轮询状态。
    const poll = () => void backend().getMCPStatus().then(setStatuses).catch(() => {});
    poll();
    const timer = setInterval(poll, 1500);
    return () => clearInterval(timer);
  }, []);

  if (!servers) return <div className="p-6 text-sm text-muted">{error || t("加载中…")}</div>;
  const current = selected >= 0 ? servers[selected] : null;
  const status = current ? statuses.find((s) => s.name === current.name) : undefined;

  const commit = async (next: MCPServerConfig[], select: number) => {
    try {
      await backend().saveMCPServers(next);
      setServers(next);
      setSelected(select);
      setError("");
      setSaved(true);
      setStatuses(await backend().getMCPStatus());
      void refreshMeta();
    } catch (e) {
      setError(errMsg(e));
      setSaved(false);
    }
  };

  const onSave = (cfg: MCPServerConfig) => {
    const next = [...servers];
    if (selected >= 0) next[selected] = cfg;
    else next.push(cfg);
    void commit(next, selected >= 0 ? selected : next.length - 1);
  };
  const onDelete = () => {
    if (!current || !confirm(t("删除服务器「{name}」？", { name: current.name }))) return;
    const next = servers.filter((_, i) => i !== selected);
    void commit(next, next.length ? 0 : -1);
  };

  return (
    <div className="flex min-h-0 flex-1">
      <aside className="flex w-60 shrink-0 flex-col border-r border-line">
        <div className="px-4 pb-2 pt-4 text-sm font-semibold">{t("MCP 服务器")}</div>
        <nav className="flex-1 overflow-y-auto px-2">
          {servers.length === 0 && <div className="px-2 py-2 text-xs text-muted">{t("还没有添加 MCP 服务器。")}</div>}
          {servers.map((s, i) => {
            const st = statuses.find((x) => x.name === s.name);
            return (
              <button
                key={s.name}
                className={`flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm ${i === selected ? "bg-panel-2" : "hover:bg-panel-2/60"}`}
                onClick={() => {
                  setSelected(i);
                  setSaved(false);
                  setError("");
                }}
              >
                <span className={`h-2 w-2 shrink-0 rounded-full ${st ? DOT[st.state] : "bg-muted/50"}`} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-mono">{s.name}</span>
                  <span className="block truncate text-[11px] text-muted">{s.transport === "http" ? s.url : s.command}</span>
                </span>
              </button>
            );
          })}
        </nav>
        <div className="border-t border-line p-3">
          <button
            className={`w-full rounded-lg border border-dashed px-3 py-2 text-sm ${selected === -1 ? "border-accent text-accent" : "border-line hover:bg-panel-2"}`}
            onClick={() => {
              setSelected(-1);
              setSaved(false);
              setError("");
            }}
          >
            {t("＋ 添加服务器")}
          </button>
        </div>
      </aside>

      <section className="flex min-w-0 flex-1 flex-col">
        {/* key 保证切换 server 时表单状态重置 */}
        <ServerForm
          key={selected >= 0 ? `s-${selected}-${current?.name}` : "new"}
          initial={current ? toForm(current) : null}
          status={status}
          error={error}
          saved={saved}
          onSave={onSave}
          onDelete={current ? onDelete : undefined}
          onReconnect={
            current
              ? () =>
                  void backend()
                    .reconnectMCP(current.name)
                    .catch((e) => setError(errMsg(e)))
              : undefined
          }
        />
      </section>
    </div>
  );
}

function ServerForm(props: {
  initial: MCPForm | null;
  status?: MCPServerStatus;
  error: string;
  saved: boolean;
  onSave: (c: MCPServerConfig) => void;
  onDelete?: () => void;
  onReconnect?: () => void;
}) {
  const [form, setForm] = useState<MCPForm>(props.initial ?? EMPTY_FORM);
  const [dirty, setDirty] = useState(false);
  const patch = (p: Partial<MCPForm>) => {
    setForm({ ...form, ...p });
    setDirty(true);
  };
  const st = props.status;
  const stateText = st
    ? { connecting: t("连接中…"), connected: t("已连接 · {n} 个工具", { n: st.tools.length }), failed: t("连接失败"), disabled: t("已停用") }[st.state]
    : "";

  return (
    <>
      <div className="flex-1 space-y-4 overflow-y-auto px-5 py-4">
        <p className="text-[11px] leading-relaxed text-muted">
          {t("MCP（Model Context Protocol）服务器为模型提供额外的工具，例如访问 GitHub、数据库、浏览器。工具名会加上 mcp__名称__ 前缀，调用时与内置工具一样经过权限确认。")}
        </p>

        {st && !dirty && (
          <div className={`rounded-lg border px-3 py-2 text-xs ${st.state === "failed" ? "border-danger/40 bg-danger/10 text-danger" : "border-line bg-panel-2"}`}>
            <div className="flex items-center gap-2">
              <span className={`h-2 w-2 rounded-full ${DOT[st.state]}`} />
              <span className="font-medium">{stateText}</span>
              {st.server_name && (
                <span className="text-muted">
                  · {st.server_name} {st.server_version}
                </span>
              )}
              <div className="flex-1" />
              {props.onReconnect && st.state !== "disabled" && (
                <button className="btn px-2 py-0.5 text-xs" onClick={props.onReconnect}>
                  {t("重新连接")}
                </button>
              )}
            </div>
            {st.error && <div className="mt-1 whitespace-pre-wrap break-all font-mono text-[11px]">{st.error}</div>}
            {st.tools.length > 0 && <div className="mt-1 break-all font-mono text-[11px] text-muted">{st.tools.join("  ")}</div>}
          </div>
        )}

        <div className="grid grid-cols-2 gap-4">
          <Field label={t("名称")}>
            <input className="input font-mono" value={form.name} onChange={(e) => patch({ name: e.target.value })} placeholder="github" />
          </Field>
          <Field label={t("传输方式")}>
            <div className="flex gap-1 rounded-lg border border-line p-1">
              {(
                [
                  ["stdio", t("本地命令（stdio）")],
                  ["http", t("远程地址（HTTP）")],
                ] as const
              ).map(([v, label]) => (
                <button
                  key={v}
                  className={`flex-1 rounded-md px-2 py-1 text-sm ${form.transport === v ? "bg-accent text-white" : "hover:bg-panel-2"}`}
                  onClick={() => patch({ transport: v })}
                >
                  {label}
                </button>
              ))}
            </div>
          </Field>
        </div>

        {form.transport === "stdio" ? (
          <>
            <Field label={t("命令")}>
              <input className="input font-mono" value={form.command} onChange={(e) => patch({ command: e.target.value })} placeholder="npx" />
            </Field>
            <Field label={t("参数（每行一个）")}>
              <textarea className="input h-20 font-mono" value={form.args} onChange={(e) => patch({ args: e.target.value })} placeholder={"-y\n@modelcontextprotocol/server-filesystem\n/path/to/dir"} />
            </Field>
            <Field label={t("环境变量（每行 KEY=VALUE）")}>
              <textarea className="input h-16 font-mono" value={form.env} onChange={(e) => patch({ env: e.target.value })} placeholder="GITHUB_TOKEN=ghp_..." spellCheck={false} />
            </Field>
          </>
        ) : (
          <>
            <Field label={t("地址")}>
              <input className="input font-mono" value={form.url} onChange={(e) => patch({ url: e.target.value })} placeholder="https://example.com/mcp" />
            </Field>
            <Field label={t("请求头（每行 Name: Value）")}>
              <textarea className="input h-16 font-mono" value={form.headers} onChange={(e) => patch({ headers: e.target.value })} placeholder="Authorization: Bearer ..." spellCheck={false} />
            </Field>
          </>
        )}
        <p className="text-[11px] text-muted">{t("环境变量和请求头保存在 settings.json 中（仅当前用户可读）。")}</p>

        <Field label={t("工具的风险等级")}>
          <select className="input" value={form.risk} onChange={(e) => patch({ risk: e.target.value as MCPForm["risk"] })}>
            <option value="exec">{t("执行（每次询问）")}</option>
            <option value="write">{t("写入（默认档位下询问）")}</option>
            <option value="read">{t("只读（默认档位下直接放行）")}</option>
          </select>
          <span className="mt-1 block text-[11px] text-muted">{t("MCP 协议没有可信的风险声明，默认按“执行”处理。只有确认服务器的所有工具都是只读的，才改成只读。")}</span>
        </Field>

        <label className="flex cursor-pointer items-center gap-2 text-sm">
          <input type="checkbox" className="accent-[var(--accent)]" checked={form.disabled} onChange={(e) => patch({ disabled: e.target.checked })} />
          {t("停用（保留配置但不连接）")}
        </label>

        {!props.initial && (
          <button className="text-xs text-accent hover:underline" onClick={() => patch(EXAMPLE_FORM)}>
            {t("示例")}: filesystem（npx @modelcontextprotocol/server-filesystem）
          </button>
        )}
      </div>

      <footer className="flex shrink-0 items-center gap-2 border-t border-line px-5 py-3">
        {props.onDelete && (
          <button className="rounded-md px-3 py-1.5 text-sm text-danger hover:bg-danger/10" onClick={props.onDelete}>
            {t("删除")}
          </button>
        )}
        <div className="flex-1" />
        {props.error ? (
          <span className="text-xs text-danger">{props.error}</span>
        ) : dirty ? (
          <span className="text-xs text-muted">{t("未保存的修改")}</span>
        ) : (
          props.saved && <span className="text-xs text-ok">{t("✓ 已保存，正在后台连接")}</span>
        )}
        <button
          className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-white hover:opacity-90"
          onClick={() => {
            props.onSave(fromForm(form));
            setDirty(false);
          }}
        >
          {t("保存并连接")}
        </button>
      </footer>
    </>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div className="mb-1.5 text-xs font-medium text-muted">{label}</div>
      {children}
    </div>
  );
}
