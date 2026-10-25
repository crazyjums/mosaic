import { useEffect, useMemo, useState, type ReactNode } from "react";
import { backend, type GroupStatus, type GroupStrategy, type GroupView, type MemberStatus, type ProviderView } from "../bridge";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";

// 设置 → 模型厂商 → 模型组：一个"虚拟厂商"，由多个"厂商 + 模型"按顺序组成。
// 请求失败时（并且还没有输出任何内容）自动换下一个成员；失败的成员进入冷却，到期后用真实请求试探恢复。
// 保存后它和普通厂商一起出现在聊天界面的模型下拉框里（模型名固定为 auto）。

const STRATEGIES: { id: GroupStrategy; label: string; hint: string }[] = [
  { id: "order", label: "按顺序（主力 + 备用）", hint: "永远从第一个可用的成员开始，它不行再用下一个。" },
  { id: "rotate", label: "轮换", hint: "每个新会话换下一个成员，适合分摊多个账号的额度。" },
  { id: "least_used", label: "优先用得最少的", hint: "优先今天用掉 token 最少的厂商，适合多个等价的账号。" },
];

const STATE_LABEL: Record<string, string> = { healthy: "正常", cooling: "冷却中", probing: "等待试探", disabled: "已停用" };
const STATE_COLOR: Record<string, string> = { healthy: "bg-ok", cooling: "bg-warn", probing: "bg-warn", disabled: "bg-danger" };
const KIND_LABEL: Record<string, string> = {
  rate_limit: "限流", server: "服务端错误", auth: "认证失败", network: "网络 / 超时", quota: "额度用完", context: "上下文超长",
};

interface Form {
  name: string;
  members: { profile: string; model: string }[];
  strategy: GroupStrategy;
  sticky: boolean;
  timeout: string;
}

const fromView = (g: GroupView | null): Form =>
  g
    ? { name: g.name, members: g.members.map((m) => ({ ...m })), strategy: g.strategy || "order", sticky: g.sticky, timeout: String(g.first_byte_timeout_s) }
    : { name: "", members: [], strategy: "order", sticky: true, timeout: "30" };

export default function GroupEditor({ name, providers, onSaved }: { name: string | null; providers: ProviderView[]; onSaved: (name: string | null) => void }) {
  const reload = useSettings((s) => s.reload);
  const refreshMeta = useChat((s) => s.refreshMeta);
  const [view, setView] = useState<GroupView | null | undefined>(undefined);
  const [form, setForm] = useState<Form>(fromView(null));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<GroupStatus | null>(null);

  // 可以当成员的配置档：不是模型组、没被标为不可用的。
  const choices = useMemo(() => providers.filter((p) => p.type !== "group" && p.available && (p.models?.length ?? 0) > 0), [providers]);

  useEffect(() => {
    let alive = true;
    void backend()
      .getGroups()
      .then((gs) => {
        if (!alive) return;
        const g = gs.find((x) => x.name === name) ?? null;
        setView(g);
        setForm(fromView(g));
      });
    return () => {
      alive = false;
    };
  }, [name]);

  // 编辑已有模型组时，每 3 秒刷新成员状态。
  useEffect(() => {
    if (!name) return;
    let alive = true;
    const poll = () =>
      void backend()
        .getGroupStatuses()
        .then((all) => alive && setStatus(all.find((s) => s.name === name) ?? null))
        .catch(() => {});
    poll();
    const timer = setInterval(poll, 3000);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [name]);

  const patch = (p: Partial<Form>) => setForm((f) => ({ ...f, ...p }));
  const setMember = (i: number, m: Partial<{ profile: string; model: string }>) =>
    patch({
      members: form.members.map((x, j) => {
        if (j !== i) return x;
        const next = { ...x, ...m };
        // 换了厂商：模型换成它的第一个
        if (m.profile && m.profile !== x.profile) next.model = choices.find((c) => c.name === m.profile)?.models?.[0] ?? "";
        return next;
      }),
    });
  const move = (i: number, d: -1 | 1) => {
    const j = i + d;
    if (j < 0 || j >= form.members.length) return;
    const ms = [...form.members];
    [ms[i], ms[j]] = [ms[j], ms[i]];
    patch({ members: ms });
  };
  const addMember = () => {
    const used = new Set(form.members.map((m) => `${m.profile}/${m.model}`));
    for (const c of choices) {
      const model = c.models?.find((m) => !used.has(`${c.name}/${m}`));
      if (model) return patch({ members: [...form.members, { profile: c.name, model }] });
    }
    setError(t("没有更多可以添加的厂商 / 模型了"));
  };

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await backend().saveGroup({
        original_name: name ?? "",
        name: form.name.trim(),
        members: form.members,
        strategy: form.strategy,
        sticky: form.sticky,
        first_byte_timeout_s: Math.max(0, Math.round(Number(form.timeout) || 0)),
      });
      await Promise.all([reload(), refreshMeta()]);
      onSaved(form.name.trim());
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!name || !confirm(t("删除模型组「{name}」？", { name }))) return;
    setBusy(true);
    try {
      await backend().deleteGroup(name);
      await Promise.all([reload(), refreshMeta()]);
      onSaved(null);
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };

  if (view === undefined) return <div className="p-6 text-sm text-muted">{t("加载中…")}</div>;
  const strat = STRATEGIES.find((s) => s.id === form.strategy)!;
  const stateOf = (m: { profile: string; model: string }): MemberStatus | undefined => status?.members?.find((x) => x.profile === m.profile && x.model === m.model);

  return (
    <>
      <div className="flex-1 space-y-5 overflow-y-auto px-5 py-4">
        <div className="rounded-lg border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-warn">
          {t("故障转移会把你的对话发给组里的其他厂商。只有你加进这个组的成员才会被使用；转交发生时，对应的消息上会有标记。")}
        </div>
        {view && !view.available && view.error && <div className="rounded-lg border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">{t("当前不可用：{e}", { e: view.error })}</div>}

        <Field label={t("名称")}>
          <input className="input" value={form.name} onChange={(e) => patch({ name: e.target.value })} placeholder={t("例如 主力")} />
        </Field>

        <Field label={t("成员（从上到下依次尝试）")}>
          <div className="space-y-2">
            {form.members.map((m, i) => {
              const st = stateOf(m);
              const profile = choices.find((c) => c.name === m.profile);
              return (
                <div key={`${i}-${m.profile}`} className="rounded-lg border border-line p-2">
                  <div className="flex items-center gap-2">
                    <span className="w-5 text-center text-xs text-muted">{i + 1}</span>
                    <select className="input flex-1 py-1" value={m.profile} onChange={(e) => setMember(i, { profile: e.target.value })} aria-label={t("厂商")}>
                      {!profile && <option value={m.profile}>{m.profile}</option>}
                      {choices.map((c) => (
                        <option key={c.name}>{c.name}</option>
                      ))}
                    </select>
                    <select className="input flex-1 py-1 font-mono" value={m.model} onChange={(e) => setMember(i, { model: e.target.value })} aria-label={t("模型")}>
                      {!(profile?.models ?? []).includes(m.model) && <option value={m.model}>{m.model}</option>}
                      {(profile?.models ?? []).map((x) => (
                        <option key={x}>{x}</option>
                      ))}
                    </select>
                    <button className="rounded px-1.5 text-muted hover:text-fg disabled:opacity-30" onClick={() => move(i, -1)} disabled={i === 0} title={t("上移")} aria-label={t("上移")}>
                      ↑
                    </button>
                    <button className="rounded px-1.5 text-muted hover:text-fg disabled:opacity-30" onClick={() => move(i, 1)} disabled={i === form.members.length - 1} title={t("下移")} aria-label={t("下移")}>
                      ↓
                    </button>
                    <button className="rounded px-1.5 text-muted hover:text-danger" onClick={() => patch({ members: form.members.filter((_, j) => j !== i) })} title={t("移除")} aria-label={t("移除")}>
                      ✕
                    </button>
                  </div>
                  {(st || !profile) && <MemberLine st={st} missing={!profile} onReset={() => void backend().resetGroupMember(m.profile, m.model).then(() => backend().getGroupStatuses().then((all) => setStatus(all.find((s) => s.name === name) ?? null)))} />}
                </div>
              );
            })}
            <button className="btn" onClick={addMember}>
              {t("＋ 添加成员")}
            </button>
          </div>
        </Field>

        <Field label={t("策略")}>
          <select className="input" value={form.strategy} onChange={(e) => patch({ strategy: e.target.value as GroupStrategy })}>
            {STRATEGIES.map((s) => (
              <option key={s.id} value={s.id}>
                {t(s.label)}
              </option>
            ))}
          </select>
          <Hint>{t(strat.hint)}</Hint>
        </Field>

        <label className="flex cursor-pointer items-start gap-2 text-sm">
          <input type="checkbox" className="mt-0.5 accent-[var(--accent)]" checked={form.sticky} onChange={(e) => patch({ sticky: e.target.checked })} />
          <span>
            {t("同一会话优先沿用上一个应答的成员")}
            <span className="block text-[11px] text-muted">{t("厂商侧的提示缓存按账号生效，来回换成员会让缓存全部失效，既慢又贵。成员失败时仍会换。")}</span>
          </span>
        </label>

        <Field label={t("等待首个响应的超时（秒）")}>
          <input className="input w-28" inputMode="numeric" value={form.timeout} onChange={(e) => patch({ timeout: e.target.value })} />
          <Hint>{t("超过这个时间还没有收到第一个事件，按网络故障处理并换下一个成员。只管第一个事件，不限制整个回答的时长；0 表示不限制。思考很久的模型请调大。")}</Hint>
        </Field>

        <details className="rounded-lg border border-line px-3 py-2 text-xs text-muted">
          <summary className="cursor-pointer text-sm">{t("什么情况下会切换")}</summary>
          <ul className="mt-2 list-disc space-y-1 pl-5 leading-relaxed">
            <li>{t("只有成员在还没有输出任何内容时失败才会切换；已经输出内容后失败，会显示错误，不会自动换（避免内容重复或工具被执行两次）。")}</li>
            <li>{t("会切换：限流（429）、服务端错误（5xx）、网络故障或超时、认证失败、额度用完、上下文超长（换窗口更大的成员）。")}</li>
            <li>{t("不会切换：请求本身有问题（其他 400 / 422）、你主动取消。")}</li>
            <li>{t("失败的成员会冷却：30 秒起，连续失败翻倍，最长 10 分钟；限流按服务器给的时间；认证失败会停用，直到你修改配置或手动恢复。冷却到期后，用一个真实请求去试探它。")}</li>
          </ul>
        </details>
      </div>

      {error && <div className="shrink-0 border-t border-danger/40 bg-danger/10 px-5 py-2 text-sm text-danger">{error}</div>}
      <footer className="flex shrink-0 items-center gap-2 border-t border-line px-5 py-3">
        {name && (
          <button className="rounded-md px-3 py-1.5 text-sm text-danger hover:bg-danger/10" onClick={() => void remove()} disabled={busy}>
            {t("删除")}
          </button>
        )}
        <div className="flex-1" />
        <button className="rounded-md bg-accent px-4 py-1.5 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" onClick={() => void save()} disabled={busy}>
          {busy ? t("保存中…") : t("保存")}
        </button>
      </footer>
    </>
  );
}

function MemberLine({ st, missing, onReset }: { st?: MemberStatus; missing: boolean; onReset: () => void }) {
  if (missing) return <div className="mt-1.5 pl-7 text-xs text-danger">{t("这个配置档不存在或不可用（可能被删除了）")}</div>;
  if (!st) return null;
  if (st.unavailable) return <div className="mt-1.5 pl-7 text-xs text-danger">{t("不可用：{e}", { e: st.unavailable })}</div>;
  const secs = st.until ? Math.max(0, Math.round((st.until - Date.now()) / 1000)) : 0;
  return (
    <div className="mt-1.5 flex items-center gap-2 pl-7 text-xs text-muted">
      <span className={`h-2 w-2 rounded-full ${STATE_COLOR[st.state] ?? "bg-muted"}`} />
      <span>{t(STATE_LABEL[st.state] ?? st.state)}</span>
      {st.state === "cooling" && <span className="tabular-nums">{t("还需约 {n} 秒", { n: secs })}</span>}
      {st.kind && st.state !== "healthy" && <span>· {t(KIND_LABEL[st.kind] ?? st.kind)}</span>}
      {st.message && st.state !== "healthy" && (
        <span className="min-w-0 flex-1 truncate" title={st.message}>
          {st.message}
        </span>
      )}
      {st.state !== "healthy" && (
        <button className="ml-auto shrink-0 text-accent hover:underline" onClick={onReset}>
          {t("恢复")}
        </button>
      )}
    </div>
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

function Hint({ children }: { children: ReactNode }) {
  return <span className="mt-1 block text-[11px] text-muted">{children}</span>;
}
