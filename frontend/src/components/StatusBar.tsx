import { useEffect, useState, type ReactNode } from "react";
import { useShallow } from "zustand/react/shallow";
import { useChat } from "../store/chatStore";
import { contextTokens, currentStep, fmtDuration, fmtTokens, fmtUSD, modelMeta, sessionCost } from "../store/metrics";
import { useSettings } from "../store/settingsStore";
import { useStatusLine, type ItemId } from "../store/statusline";
import PermissionModeSelect from "./PermissionModeSelect";

// 输入框下方的状态栏。显示哪些项、按什么顺序由状态栏配置决定（设置 → 状态栏）。
//
// 每一项是一个独立的小组件，返回 null 表示"此刻没有可显示的内容"（例如工作区不是 git 仓库），
// 这样关掉一项、或某项暂时没数据时，分隔线也不会多出来。

export default function StatusBar() {
  const config = useStatusLine((s) => s.config);
  const load = useStatusLine((s) => s.load);
  const openSettings = useSettings((s) => s.show);
  useEffect(() => void load(), [load]);

  const rendered = config.items
    .filter((it) => it.enabled)
    .map((it) => ({ id: it.id, node: <Item id={it.id} /> }));

  return (
    <div className="flex w-full flex-wrap items-center gap-y-1 px-6 py-2 text-xs text-muted">
      {rendered.map(({ id, node }) => (
        // statusline-item 的分隔线和"空项隐藏"写在 index.css 里
        <span key={id} className="statusline-item">
          {node}
        </span>
      ))}
      <button className="ml-auto pl-3 text-muted/70 hover:text-fg" title="自定义状态栏" onClick={() => void openSettings("statusline")}>
        ⚙
      </button>
    </div>
  );
}

function Item({ id }: { id: ItemId }) {
  switch (id) {
    case "model":
      return <ModelItem />;
    case "tokens":
      return <TokensItem />;
    case "context":
      return <ContextItem />;
    case "cost":
      return <CostItem />;
    case "turn":
      return <TurnItem />;
    case "workspace":
      return <WorkspaceItem />;
    case "git":
      return <GitItem />;
    case "permission":
      return <PermissionModeSelect />;
    case "running":
      return <RunningItem />;
  }
}

function Tip({ tip, children, className = "" }: { tip: string; children: ReactNode; className?: string }) {
  return (
    <span className={`whitespace-nowrap ${className}`} title={tip}>
      {children}
    </span>
  );
}

function ModelItem() {
  const { provider, model, profiles, running, setModel } = useChat();
  const openSettings = useSettings((s) => s.show);
  const isDemo = profiles.find((p) => p.name === provider)?.type === "fake";

  // 选项值编码为 "profile\u0000model"，一个下拉框同时选择厂商和模型。
  const value = `${provider}\u0000${model}`;
  return (
    <span className="flex items-center gap-2">
      <select
        className="max-w-[16rem] truncate rounded border border-line bg-panel px-1.5 py-0.5 text-fg disabled:opacity-60"
        value={value}
        onChange={(e) => {
          const [p, m] = e.target.value.split("\u0000");
          void setModel(p, m);
        }}
        disabled={running}
        title="切换厂商与模型（下一条消息生效，会被记住）"
      >
        {!profiles.some((p) => p.name === provider) && <option value={value}>{provider}/{model}</option>}
        {profiles.map((p) => (
          <optgroup key={p.name} label={p.available ? `${p.name}（${p.type}）` : `${p.name}（不可用：${p.error ?? ""}）`}>
            {(p.models ?? []).map((m) => (
              <option key={m} value={`${p.name}\u0000${m}`} disabled={!p.available}>
                {p.name} / {m}
              </option>
            ))}
          </optgroup>
        ))}
      </select>
      {isDemo && (
        <button className="shrink-0 rounded bg-warn/15 px-1.5 py-0.5 text-warn hover:bg-warn/25" onClick={() => void openSettings("providers")}>
          演示模型 · 配置真实模型 →
        </button>
      )}
    </span>
  );
}

function TokensItem() {
  const u = useChat((s) => s.sessionUsage);
  const showCache = useStatusLine((s) => s.config.showCache);
  return (
    <Tip tip={`本会话累计：输入 ${u.input_tokens} / 输出 ${u.output_tokens} tokens${u.cache_read_tokens ? `，缓存命中 ${u.cache_read_tokens}` : ""}`}>
      ↑{fmtTokens(u.input_tokens)} ↓{fmtTokens(u.output_tokens)}
      {showCache && !!u.cache_read_tokens && <span className="text-ok"> · 缓存 {fmtTokens(u.cache_read_tokens)}</span>}
    </Tip>
  );
}

function ContextItem() {
  const tokens = useChat(contextTokens);
  const ctxWindow = useChat((s) => modelMeta(s.profiles, s.provider, s.model)?.context_window);
  if (tokens === null) return null;
  if (!ctxWindow) {
    return <Tip tip="最近一次请求的上下文大小。在 设置 → 模型厂商 → 高级选项 中填写该模型的上下文窗口，可以显示占比。">上下文 {fmtTokens(tokens)}</Tip>;
  }
  const pct = Math.min(100, (tokens / ctxWindow) * 100);
  const color = pct >= 85 ? "bg-danger" : pct >= 60 ? "bg-warn" : "bg-ok";
  return (
    <Tip tip={`最近一次请求的上下文：${tokens} / ${ctxWindow} tokens（${pct.toFixed(1)}%）`} className="flex items-center gap-1.5">
      上下文
      <span className="inline-block h-1.5 w-14 overflow-hidden rounded-full bg-line">
        <span className={`block h-full ${color}`} style={{ width: `${Math.max(pct, 2)}%` }} />
      </span>
      {fmtTokens(tokens)} / {fmtTokens(ctxWindow)}
    </Tip>
  );
}

function CostItem() {
  // sessionCost 每次返回新对象；useShallow 按字段比较，避免每次都触发重渲染（Zustand v5 会因此报错）。
  const cost = useChat(useShallow((s) => sessionCost(s, s.profiles) ?? { usd: -1, partial: false }));
  if (cost.usd < 0) return null;
  return (
    <Tip tip={cost.partial ? "预估花费（部分调用的模型没有配置单价，或基于会话累计用量近似计算）" : "按模型单价估算的本会话花费"}>
      ≈ {fmtUSD(cost.usd)}
      {cost.partial && "+"}
    </Tip>
  );
}

function TurnItem() {
  const running = useChat((s) => s.running);
  const startedAt = useChat((s) => s.turnStartedAt);
  const lastMs = useChat((s) => s.lastTurnMs);
  const step = useChat(currentStep);
  // 运行中每秒刷新一次，显示实时耗时
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [running]);

  if (running && startedAt !== null) {
    return (
      <Tip tip="本轮已用时间 / 当前是第几次模型调用" className="text-accent">
        ⏱ {fmtDuration(now - startedAt)}
        {step !== null && ` · 第 ${step} 步`}
      </Tip>
    );
  }
  if (lastMs === null) return null;
  return <Tip tip="上一轮的总耗时">⏱ 上一轮 {fmtDuration(lastMs)}</Tip>;
}

function WorkspaceItem() {
  const path = useChat((s) => s.workspace);
  const name = useChat((s) => s.workspaceInfo?.name);
  const style = useStatusLine((s) => s.config.workspaceStyle);
  if (!path) return null;
  return (
    <Tip tip={`工作区：${path}`} className="max-w-[20rem] truncate">
      📁 {style === "full" ? path : name || path}
    </Tip>
  );
}

function GitItem() {
  const info = useChat((s) => s.workspaceInfo);
  if (!info?.is_repo) return null;
  return (
    <Tip tip={info.dirty ? `分支 ${info.branch}，${info.changed_files} 个文件有未提交的改动` : `分支 ${info.branch}，没有未提交的改动`}>
      ⎇ {info.branch}
      {info.dirty && <span className="text-warn"> ●{info.changed_files}</span>}
    </Tip>
  );
}

function RunningItem() {
  const running = useChat((s) => s.running);
  const waiting = useChat((s) => s.permissions.length > 0);
  const [label, dot, color] = waiting
    ? ["等待确认", "bg-warn animate-pulse", "text-warn"]
    : running
      ? ["运行中", "bg-accent animate-pulse", "text-accent"]
      : ["空闲", "bg-ok", ""];
  return (
    <span className={`flex items-center gap-1 whitespace-nowrap ${color}`}>
      <span className={`inline-block h-1.5 w-1.5 rounded-full ${dot}`} />
      {label}
    </span>
  );
}
