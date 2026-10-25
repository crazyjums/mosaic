import { useCallback, useEffect, useMemo, useState } from "react";
import { backend, type UsageBy, type UsageNode, type UsageRange, type UsageReport } from "../bridge";
import { t } from "../i18n";
import { useSettings } from "../store/settingsStore";
import { useUIPrefs } from "../store/uiPrefs";
import { fmtCost, fmtDay, fmtPct, fmtTokens, nodeCost } from "../store/usageFormat";

// 设置 → 用量：用了多少 token、花了多少钱，按天展开。
//
// 数据来自后端的用量账本（逐次调用的记录，只有数字和名称）。输入 token 的口径是"含缓存读写"；
// 花费是按配置档里填的价格在读取时重算的"预估"：没有价格的调用不计入、也不当作免费。

const RANGES: [UsageRange, string][] = [
  ["today", "今天"],
  ["7d", "7 天"],
  ["30d", "30 天"],
  ["all", "全部"],
];
const BYS: [UsageBy, string][] = [
  ["day", "按天"],
  ["model", "按模型"],
  ["profile", "按厂商"],
  ["session", "按会话"],
];
const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

export default function UsageSettings() {
  const prefs = useUIPrefs((s) => s.prefs);
  const updatePrefs = useUIPrefs((s) => s.update);
  const setTab = useSettings((s) => s.setTab);
  const [range, setRange] = useState<UsageRange>("7d");
  const [by, setBy] = useState<UsageBy>("day");
  const [profile, setProfile] = useState("");
  const [report, setReport] = useState<UsageReport | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [saved, setSaved] = useState("");
  const { currency, cnyRate } = prefs;

  const req = useMemo(() => ({ range, by, profile: profile || undefined }), [range, by, profile]);
  const load = useCallback(async () => {
    setLoading(true);
    try {
      setReport(await backend().getUsageReport(req));
      setError("");
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      setLoading(false);
    }
  }, [req]);
  useEffect(() => {
    void load();
  }, [load]);

  const exportCSV = async () => {
    setSaved("");
    try {
      const path = await backend().saveUsageCSV(req);
      if (path) setSaved(path);
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    }
  };

  const tot = report?.totals;
  const money = (usd: number) => fmtCost(usd, currency, cnyRate);

  return (
    <div className="flex-1 space-y-4 overflow-y-auto px-6 py-5">
      <div className="flex flex-wrap items-center gap-3">
        <Seg value={range} options={RANGES} onChange={setRange} />
        <Seg value={by} options={BYS} onChange={setBy} />
        {report && report.profiles.length > 1 && (
          <select className="input w-auto py-1" value={profile} onChange={(e) => setProfile(e.target.value)} aria-label={t("筛选厂商")}>
            <option value="">{t("全部厂商")}</option>
            {report.profiles.map((p) => (
              <option key={p}>{p}</option>
            ))}
          </select>
        )}
        <div className="flex-1" />
        <div className="flex items-center gap-1 text-xs">
          <Seg value={currency} options={[["usd", "USD"], ["cny", "CNY"]]} onChange={(c) => void updatePrefs({ currency: c })} />
          {currency === "cny" && (
            <label className="flex items-center gap-1 text-muted" title={t("汇率由你填写，不会联网获取")}>
              1$ =
              <input
                className="input w-16 px-1.5 py-0.5 text-right text-xs"
                inputMode="decimal"
                defaultValue={cnyRate}
                onBlur={(e) => {
                  const v = parseFloat(e.target.value);
                  if (Number.isFinite(v) && v > 0 && v < 1000) void updatePrefs({ cnyRate: v });
                  else e.target.value = String(cnyRate);
                }}
                aria-label={t("汇率")}
              />
            </label>
          )}
        </div>
        <button className="btn py-1" onClick={() => void exportCSV()} disabled={!report}>
          {t("导出 CSV")}
        </button>
      </div>
      {saved && <div className="text-xs text-ok">{t("已导出到 {path}", { path: saved })}</div>}
      {error && <div className="rounded-md border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">{error}</div>}

      {tot && (
        <>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
            <Card label={t("输入")} value={fmtTokens(tot.input)} hint={t("含缓存读写")} />
            <Card label={t("输出")} value={fmtTokens(tot.output)} />
            <Card label={t("缓存命中")} value={tot.input > 0 ? fmtPct(tot.cache_hit) : "—"} />
            <Card label={t("请求")} value={String(tot.requests)} />
            <Card label={t("预估花费")} value={tot.cost_usd > 0 || tot.unpriced_calls === 0 ? money(tot.cost_usd) : "—"} />
          </div>

          <div className="space-y-1 text-xs text-muted">
            {tot.unpriced_calls > 0 && (
              <div className="text-warn">
                {t("有 {n} 次调用没有价格，未计入花费（不等于免费）：{models}。", { n: tot.unpriced_calls, models: report.unpriced_models.join("、") })}{" "}
                <button className="text-accent hover:underline" onClick={() => setTab("providers")}>
                  {t("去设置价格")}
                </button>
              </div>
            )}
            {tot.plan_calls > 0 && <div>{t("{n} 次调用走的是套餐额度（ChatGPT 账号），只统计 token，不计金额。", { n: tot.plan_calls })}</div>}
            {tot.failed_tries > 0 && (
              <div>{t("另有 {n} 次失败的尝试，其中 {m} 次已转交给模型组里的其他成员；它们不算请求。", { n: tot.failed_tries, m: tot.handed_on })}</div>
            )}
            <div>{t("花费是按各配置档里填的价格算出的预估，改价格后历史花费会同步变化。")}</div>
          </div>

          {by === "day" && report.nodes.length > 0 && <DayChart nodes={report.nodes} />}

          <div className="overflow-x-auto rounded-lg border border-line">
            <div className="min-w-[520px] text-sm">
              <Row head cells={[by === "session" ? t("会话") : by === "day" ? t("日期") : t("名称"), t("输入"), t("输出"), t("缓存命中"), t("请求"), t("花费")]} />
              {report.nodes.length === 0 && <div className="px-3 py-6 text-center text-muted">{loading ? t("加载中…") : t("这个时间范围里没有调用记录")}</div>}
              {report.nodes.map((n) => (
                <NodeRow key={n.key || "_"} node={n} by={by} money={nodeCostFn(currency, cnyRate, t("套餐"))} />
              ))}
            </div>
          </div>
          <p className="text-[11px] leading-relaxed text-muted">
            {t("只记录数字（token、耗时、模型名），不保存任何对话内容。删除会话或清空历史不会清掉用量统计。账本从升级到这个版本那天开始记录。")}
          </p>
        </>
      )}
      {!report && !error && <div className="text-sm text-muted">{t("加载中…")}</div>}
    </div>
  );
}

const nodeCostFn = (currency: "usd" | "cny", rate: number, plan: string) => (n: UsageNode) => nodeCost(n, currency, rate, plan);

function Seg<T extends string>({ value, options, onChange }: { value: T; options: [T, string][]; onChange: (v: T) => void }) {
  return (
    <div className="inline-flex overflow-hidden rounded-lg border border-line text-sm" role="radiogroup">
      {options.map(([v, label]) => (
        <button
          key={v}
          role="radio"
          aria-checked={value === v}
          className={`px-3 py-1 ${value === v ? "bg-accent text-on-accent" : "text-muted hover:bg-panel-2 hover:text-fg"}`}
          onClick={() => onChange(v)}
        >
          {/^[A-Z]+$/.test(label) ? label : t(label)}
        </button>
      ))}
    </div>
  );
}

function Card({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="min-w-0 rounded-lg bg-panel-2 px-3 py-2">
      <div className="text-[11px] text-muted">
        {label}
        {hint && <span className="ml-1 opacity-70">· {hint}</span>}
      </div>
      <div className="truncate text-lg font-semibold tabular-nums">{value}</div>
    </div>
  );
}

const GRID = "grid grid-cols-[minmax(150px,2.4fr)_repeat(5,minmax(60px,1fr))] items-center gap-2 px-3 py-1.5";

function Row({ cells, head }: { cells: string[]; head?: boolean }) {
  return (
    <div className={`${GRID} ${head ? "border-b border-line text-xs text-muted" : ""}`}>
      {cells.map((c, i) => (
        <span key={i} className={`truncate ${i > 0 ? "text-right tabular-nums" : ""}`}>
          {c}
        </span>
      ))}
    </div>
  );
}

function cells(n: UsageNode, label: string, cost: string): string[] {
  return [label, fmtTokens(n.input), fmtTokens(n.output), n.input > 0 ? fmtPct(n.cache_hit) : "—", String(n.requests), cost];
}

// 一行汇总，点开看明细（按天时是按模型，按会话时也是按模型）。用原生 details，键盘和读屏都可用。
function NodeRow({ node, by, money }: { node: UsageNode; by: UsageBy; money: (n: UsageNode) => string }) {
  const label = by === "day" ? fmtDay(node.label, WEEKDAYS.map((w) => t(w))) : node.label;
  const failedNote = node.failed_tries > 0 ? ` · ${t("失败 {n}", { n: node.failed_tries })}` : "";
  if (!node.children?.length) {
    return (
      <div className="border-t border-line first:border-t-0">
        <Row cells={cells(node, label + failedNote, money(node))} />
      </div>
    );
  }
  return (
    <details className="group border-t border-line first:border-t-0">
      <summary className={`${GRID} cursor-pointer list-none font-medium hover:bg-panel-2 [&::-webkit-details-marker]:hidden`}>
        {cells(node, label + failedNote, money(node)).map((c, i) => (
          <span key={i} className={`truncate ${i > 0 ? "text-right font-normal tabular-nums" : ""}`}>
            {i === 0 && <span className="mr-1 inline-block w-3 text-muted transition-transform group-open:rotate-90">▸</span>}
            {c}
          </span>
        ))}
      </summary>
      <div className="bg-panel-2/40 text-muted">
        {node.children.map((c) => (
          <div key={c.key} className="[&>div>span:first-child]:pl-5">
            <Row cells={cells(c, c.label + (c.failed_tries > 0 ? ` · ${t("失败 {n}", { n: c.failed_tries })}` : ""), money(c))} />
          </div>
        ))}
      </div>
    </details>
  );
}

// 每天一根堆叠条：普通输入 / 缓存命中 / 输出。自己画 SVG，不引入图表库。最多显示最近 60 天。
function DayChart({ nodes }: { nodes: UsageNode[] }) {
  const days = [...nodes].slice(0, 60).reverse();
  const parts = (n: UsageNode) => [
    { key: "plain", label: t("输入（未命中缓存）"), v: Math.max(0, n.input - n.cache_read), color: "var(--accent)" },
    { key: "cache", label: t("输入（命中缓存）"), v: n.cache_read, color: "color-mix(in srgb, var(--accent) 40%, var(--bg))" },
    { key: "out", label: t("输出"), v: n.output, color: "var(--think)" },
  ];
  const max = Math.max(1, ...days.map((n) => parts(n).reduce((a, p) => a + p.v, 0)));
  const W = 600, H = 110, gap = 3;
  // 每根最宽 36：只有一两天数据时，不要拉成一整面色块。
  const bw = Math.min(36, Math.max(4, (W - gap * (days.length - 1)) / days.length));
  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H + 16}`} className="h-36 w-full" role="img" aria-label={t("每天的 token 用量")} preserveAspectRatio="xMinYMax meet">
        <line x1="0" x2={W} y1={H} y2={H} stroke="var(--border)" />
        {days.map((n, i) => {
          let y = H;
          const x = i * (bw + gap);
          return (
            <g key={n.key}>
              <title>{`${n.label}  ${t("输入")} ${fmtTokens(n.input)} · ${t("输出")} ${fmtTokens(n.output)} · ${t("请求")} ${n.requests}`}</title>
              {parts(n).map((p) => {
                const h = (p.v / max) * (H - 4);
                y -= h;
                return h > 0 ? <rect key={p.key} x={x} y={y} width={bw} height={h} fill={p.color} /> : null;
              })}
              {(days.length <= 10 || i === 0 || i === days.length - 1) && (
                <text x={x + bw / 2} y={H + 12} textAnchor="middle" fontSize="9" fill="var(--muted)">
                  {n.label.slice(5)}
                </text>
              )}
            </g>
          );
        })}
      </svg>
      <div className="flex flex-wrap gap-3 text-[11px] text-muted">
        {parts(days[0] ?? ({} as UsageNode)).map((p) => (
          <span key={p.key} className="flex items-center gap-1">
            <span className="inline-block h-2 w-2 rounded-sm" style={{ background: p.color }} />
            {p.label}
          </span>
        ))}
      </div>
    </div>
  );
}
