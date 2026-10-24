import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";

const fmt = (n: number) => (n >= 10000 ? `${(n / 1000).toFixed(1)}k` : String(n));

// 输入框下方的状态栏：厂商/模型选择、会话累计 token、工作区、运行状态。
export default function StatusBar() {
  const { provider, model, profiles, sessionUsage, running, workspace, setModel } = useChat();
  const openSettings = useSettings((s) => s.show);
  const isDemo = profiles.find((p) => p.name === provider)?.type === "fake";

  // 选项值编码为 "profile\u0000model"，一个下拉框同时选择厂商和模型。
  const value = `${provider}\u0000${model}`;
  const onChange = (v: string) => {
    const [p, m] = v.split("\u0000");
    void setModel(p, m);
  };

  return (
    <div className="mx-auto flex w-full max-w-3xl items-center gap-4 px-6 py-2 text-xs text-muted">
      <select
        className="max-w-[16rem] truncate rounded border border-line bg-panel px-1.5 py-0.5 text-fg disabled:opacity-60"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={running}
        title="切换厂商与模型（下一条消息生效）"
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
        <button className="shrink-0 rounded bg-warn/15 px-1.5 py-0.5 text-warn hover:bg-warn/25" onClick={() => void openSettings()}>
          演示模型 · 配置真实模型 →
        </button>
      )}
      <span title="会话累计 token（输入 / 输出）">
        tokens {fmt(sessionUsage.input_tokens)} / {fmt(sessionUsage.output_tokens)}
        {!!sessionUsage.cache_read_tokens && ` · 缓存 ${fmt(sessionUsage.cache_read_tokens)}`}
      </span>
      <span className="min-w-0 flex-1 truncate text-right" title={workspace}>
        {workspace}
      </span>
      <span className={`flex items-center gap-1 ${running ? "text-accent" : ""}`}>
        <span className={`inline-block h-1.5 w-1.5 rounded-full ${running ? "animate-pulse bg-accent" : "bg-ok"}`} />
        {running ? "运行中" : "空闲"}
      </span>
    </div>
  );
}
