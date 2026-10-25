import { useEffect, useRef, useState, type ReactNode } from "react";
import { useShallow } from "zustand/react/shallow";
import { backend } from "../bridge";
import { t } from "../i18n";
import { parseAnsi } from "../store/ansi";
import { useChat } from "../store/chatStore";
import type { ChatGPTUsage } from "../bridge";
import { contextTokens, currentStep, fmtDuration, fmtLatency, fmtTokens, fmtUSD, modelMeta, sessionCost, ttft } from "../store/metrics";
import { useSettings } from "../store/settingsStore";
import { useStatusLine, type ItemId } from "../store/statusline";
import PermissionModeSelect from "./PermissionModeSelect";
import WorkspacePicker from "./WorkspacePicker";

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
      <button className="ml-auto pl-3 text-muted/70 hover:text-fg" title={t("自定义状态栏")} onClick={() => void openSettings("statusline")}>
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
    case "chatgpt":
      return <ChatGPTQuotaItem />;
    case "turn":
      return <TurnItem />;
    case "ttft":
      return <TTFTItem />;
    case "workspace":
      return <WorkspaceItem />;
    case "git":
      return <GitItem />;
    case "permission":
      return <PermissionModeSelect />;
    case "running":
      return <RunningItem />;
    case "script":
      return <ScriptItem />;
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
        title={t("切换厂商与模型（下一条消息生效，会被记住）")}
      >
        {!profiles.some((p) => p.name === provider) && <option value={value}>{provider}/{model}</option>}
        {profiles.map((p) => (
          <optgroup key={p.name} label={p.available ? `${p.name}（${p.type}）` : p.name + t("（不可用：{error}）", { error: p.error ?? "" })}>
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
          {t("演示模型 · 配置真实模型 →")}
        </button>
      )}
    </span>
  );
}

function TokensItem() {
  const u = useChat((s) => s.sessionUsage);
  const showCache = useStatusLine((s) => s.config.showCache);
  return (
    <Tip
      tip={
        t("本会话累计：输入 {input} / 输出 {output} tokens", { input: u.input_tokens, output: u.output_tokens }) +
        (u.cache_read_tokens ? t("，缓存命中 {n}", { n: u.cache_read_tokens }) : "")
      }
    >
      ↑{fmtTokens(u.input_tokens)} ↓{fmtTokens(u.output_tokens)}
      {showCache && !!u.cache_read_tokens && <span className="text-ok">{t(" · 缓存 {n}", { n: fmtTokens(u.cache_read_tokens) })}</span>}
    </Tip>
  );
}

function ContextItem() {
  const tokens = useChat(contextTokens);
  const ctxWindow = useChat((s) => modelMeta(s.profiles, s.provider, s.model)?.context_window);
  if (tokens === null) return null;
  if (!ctxWindow) {
    return (
      <Tip tip={t("最近一次请求的上下文大小。在 设置 → 模型厂商 → 高级选项 中填写该模型的上下文窗口，可以显示占比。")}>
        {t("上下文 {n}", { n: fmtTokens(tokens) })}
      </Tip>
    );
  }
  const pct = Math.min(100, (tokens / ctxWindow) * 100);
  const color = pct >= 85 ? "bg-danger" : pct >= 60 ? "bg-warn" : "bg-ok";
  return (
    <Tip tip={t("最近一次请求的上下文：{tokens} / {window} tokens（{pct}%）", { tokens, window: ctxWindow, pct: pct.toFixed(1) })} className="flex items-center gap-1.5">
      {t("上下文")}
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
    <Tip tip={cost.partial ? t("预估花费（部分调用的模型没有配置单价，或基于会话累计用量近似计算）") : t("按模型单价估算的本会话花费")}>
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
      <Tip tip={t("本轮已用时间 / 当前是第几次模型调用")} className="text-accent">
        ⏱ {fmtDuration(now - startedAt)}
        {step !== null && t(" · 第 {n} 步", { n: step })}
      </Tip>
    );
  }
  if (lastMs === null) return null;
  return <Tip tip={t("上一轮的总耗时")}>{t("⏱ 上一轮 {d}", { d: fmtDuration(lastMs) })}</Tip>;
}

// 工作区：点击可以为当前会话选择工作区（见 WorkspacePicker）。
function WorkspaceItem() {
  const style = useStatusLine((s) => s.config.workspaceStyle);
  return <WorkspacePicker variant="statusbar" full={style === "full"} />;
}

function GitItem() {
  const info = useChat((s) => s.workspaceInfo);
  if (!info?.is_repo) return null;
  return (
    <Tip
      tip={
        info.dirty
          ? t("分支 {branch}，{n} 个文件有未提交的改动", { branch: info.branch ?? "", n: info.changed_files })
          : t("分支 {branch}，没有未提交的改动", { branch: info.branch ?? "" })
      }
    >
      ⎇ {info.branch}
      {info.dirty && <span className="text-warn"> ●{info.changed_files}</span>}
    </Tip>
  );
}

function RunningItem() {
  const running = useChat((s) => s.running);
  const waiting = useChat((s) => s.permissions.length > 0);
  const [label, dot, color] = waiting
    ? [t("等待确认"), "bg-warn animate-pulse", "text-warn"]
    : running
      ? [t("运行中"), "bg-accent animate-pulse", "text-accent"]
      : [t("空闲"), "bg-ok", ""];
  return (
    <span className={`flex items-center gap-1 whitespace-nowrap ${color}`}>
      <span className={`inline-block h-1.5 w-1.5 rounded-full ${dot}`} />
      {label}
    </span>
  );
}

// 自定义脚本的输出（设置 → 状态栏 → 自定义脚本）。
//
// 刷新时机：会话、模型、运行状态、用量变化时（最多每秒一次），以及每 15 秒一次（脚本里可能有时间、git 状态等）。
// 同一时间只跑一个：脚本慢的时候不会越积越多。命令由后端从设置文件读取，这里只传附加信息。
function ScriptItem() {
  const script = useStatusLine((s) => s.script);
  const deps = useChat(
    useShallow((s) => [s.sessionId, s.provider, s.model, s.running, s.sessionUsage.output_tokens, s.workspace, s.permissionMode, s.title]),
  );
  const [out, setOut] = useState<{ text: string; error?: string }>({ text: "" });
  const busy = useRef(false);
  const lastRun = useRef(0);

  useEffect(() => {
    if (!script) return;
    const run = async () => {
      if (busy.current) return;
      busy.current = true;
      lastRun.current = Date.now();
      const s = useChat.getState();
      const cost = sessionCost(s, s.profiles);
      try {
        const text = await backend().runStatusLine({
          title: s.title,
          running: s.running,
          permission_mode: s.permissionMode,
          context_tokens: contextTokens(s),
          context_window: modelMeta(s.profiles, s.provider, s.model)?.context_window ?? null,
          cost_usd: cost?.usd ?? null,
          git_branch: s.workspaceInfo?.branch ?? null,
        });
        setOut({ text });
      } catch (e) {
        setOut({ text: "", error: String(e instanceof Error ? e.message : e) });
      } finally {
        busy.current = false;
      }
    };
    const wait = Math.max(0, 1000 - (Date.now() - lastRun.current));
    const timer = setTimeout(() => void run(), wait);
    const interval = setInterval(() => void run(), 15000);
    return () => {
      clearTimeout(timer);
      clearInterval(interval);
    };
  }, [script, ...deps]);

  if (!script) return null;
  if (out.error) {
    return (
      <Tip tip={t("状态栏脚本出错：{error}", { error: out.error })} className="text-danger">
        ⚠ script
      </Tip>
    );
  }
  if (!out.text) return null;
  return (
    <span className="max-w-[28rem] truncate whitespace-pre" title={out.text.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, "")}>
      {parseAnsi(out.text).map((seg, i) => (
        <span
          key={i}
          style={{ color: seg.fg, backgroundColor: seg.bg }}
          className={[seg.bold && "ansi-bold", seg.dim && "ansi-dim", seg.italic && "ansi-italic", seg.underline && "ansi-underline"].filter(Boolean).join(" ")}
        >
          {seg.text}
        </span>
      ))}
    </span>
  );
}

// 首字延迟：最近一次调用从发出请求到收到第一个内容的时间。等待中实时计时（超过 1 秒才显示，避免闪烁）。
function TTFTItem() {
  const info = useChat(useShallow((s) => ttft(s)));
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (info.waitingSince === null) return;
    const timer = setInterval(() => setNow(Date.now()), 200);
    return () => clearInterval(timer);
  }, [info.waitingSince]);

  const tip = info.avgMs !== null ? t("首字延迟：最近一次 {last}，本会话平均 {avg}（{n} 次调用）", { last: fmtLatency(info.lastMs ?? 0), avg: fmtLatency(info.avgMs), n: info.count }) : t("首字延迟：从发出请求到收到第一个字（含思考）的时间");
  if (info.waitingSince !== null && now - info.waitingSince > 1000) {
    return (
      <Tip tip={tip} className="text-accent">
        {t("首字 等待 {d}…", { d: fmtLatency(now - info.waitingSince) })}
      </Tip>
    );
  }
  if (info.lastMs === null) return null;
  const color = info.lastMs >= 10000 ? "text-danger" : info.lastMs >= 3000 ? "text-warn" : "";
  return (
    <Tip tip={tip} className={color}>
      {t("首字 {d}", { d: fmtLatency(info.lastMs) })}
    </Tip>
  );
}

// ChatGPT 额度：只在当前使用 ChatGPT 账号（chatgpt 类型的厂商）时显示。
//
// OpenAI 目前没有提供查询剩余额度的接口，所以这里显示的是本应用自己记的账：最近 5 小时 / 7 天用掉的 token
// （套餐限额就是按这两个窗口计算的），以及最近一次请求是否遇到"额度已用完"。剩余额度要点击后在 ChatGPT 网页上看。
function ChatGPTQuotaItem() {
  const isChatGPT = useChat((s) => s.profiles.find((p) => p.name === s.provider)?.type === "chatgpt");
  const refreshKey = useChat((s) => `${s.lastTurnMs}|${s.running}|${s.sessionId}`);
  const [u, setU] = useState<ChatGPTUsage | null>(null);

  useEffect(() => {
    if (!isChatGPT) return;
    const load = () => void backend().getChatGPTUsage().then(setU).catch(() => {});
    load();
    const timer = setInterval(load, 30000);
    return () => clearInterval(timer);
  }, [isChatGPT, refreshKey]);

  if (!isChatGPT || !u?.logged_in) return null;
  const total = (w: ChatGPTUsage["last_5h"]) => w.input_tokens + w.output_tokens;
  const open = () => void backend().openChatGPTUsage();
  const tip = [
    t("本应用通过 ChatGPT 账号用掉的套餐额度："),
    t("最近 5 小时：{n} 次请求，输入 {i} / 输出 {o} tokens", { n: u.last_5h.requests, i: u.last_5h.input_tokens, o: u.last_5h.output_tokens }),
    t("最近 7 天：{n} 次请求，输入 {i} / 输出 {o} tokens", { n: u.last_7d.requests, i: u.last_7d.input_tokens, o: u.last_7d.output_tokens }),
    t("OpenAI 目前没有提供查询剩余额度的接口；点击在浏览器中打开 ChatGPT 的用量页面查看。"),
  ].join("\n");

  if (u.limit_hit_at) {
    return (
      <button className="whitespace-nowrap text-danger hover:underline" title={`${u.limit_message ?? ""}\n\n${tip}`} onClick={open}>
        {t("⚠ GPT 额度已用完")}
      </button>
    );
  }
  return (
    <button className="whitespace-nowrap hover:text-fg hover:underline" title={tip} onClick={open}>
      {t("GPT 5h {h} · 7d {d}", { h: fmtTokens(total(u.last_5h)), d: fmtTokens(total(u.last_7d)) })}
    </button>
  );
}
