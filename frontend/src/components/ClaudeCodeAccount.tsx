import { useEffect, useState } from "react";
import { backend, type ClaudeCodeStatus } from "../bridge";
import { copyText } from "./Markdown";

/** 仅帮助用户在官方 CLI 中使用自己的订阅登录，不以 OAuth Token 直连第三方 API。 */
export default function ClaudeCodeAccount() {
  const [status, setStatus] = useState<ClaudeCodeStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const refresh = async () => {
    setBusy(true);
    try { setStatus(await backend().getClaudeCodeStatus()); }
    catch (e) { setMessage(String(e instanceof Error ? e.message : e)); }
    finally { setBusy(false); }
  };
  useEffect(() => { void refresh(); }, []);
  const launch = async () => {
    setMessage("");
    try { await backend().launchClaudeCodeLogin(); setMessage("已请求打开官方 Claude Code 终端，按提示登录后点“刷新状态”。"); }
    catch (e) { setMessage(String(e instanceof Error ? e.message : e)); }
  };
  return (
    <div className="space-y-5 text-sm">
      <div className="rounded-card border border-line bg-panel-2 p-4">
        <h3 className="font-semibold">Claude Code · 订阅登录</h3>
        <p className="mt-2 leading-relaxed text-muted">使用本机安装的官方 Claude Code CLI 登录 Claude Pro / Max 等订阅。Mosaic 不接触或保存你的登录令牌。</p>
        <div className="mt-3 flex items-center gap-2">
          <span className={`h-2 w-2 rounded-full ${status?.logged_in ? "bg-ok" : "bg-warn"}`} />
          <span>{status?.message ?? "正在检查 Claude Code…"}</span>
          {status?.auth_method && <span className="rounded bg-panel px-2 py-0.5 font-mono text-xs text-muted">{status.auth_method}</span>}
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn-primary" onClick={() => void launch()} disabled={!status?.installed}>在终端打开官方登录</button>
        <button className="btn" onClick={() => void refresh()} disabled={busy}>{busy ? "检查中…" : "刷新状态"}</button>
        <button className="btn" onClick={() => void backend().openExternalURL("https://code.claude.com/docs/en/setup")}>安装指南</button>
      </div>
      <div className="rounded-card border border-line bg-panel px-4 py-3">
        <div className="font-medium">也可以手动登录</div>
        <p className="mt-1 text-muted">打开终端输入 <code>claude</code>，进入交互界面后输入 <code>/login</code>，选择使用 Claude 订阅账户。</p>
        <button className="mt-2 text-xs text-accent hover:underline" onClick={() => void copyText("claude")}>复制 claude 命令</button>
      </div>
      <p className="text-xs leading-relaxed text-muted">注意：此入口仅管理官方 CLI 的登录状态。Claude 订阅并不是 Anthropic API Key，不能作为 Mosaic 的 Anthropic Provider 直接请求或承诺使用订阅额度。Mosaic 如需调用 Anthropic API，仍需在“模型厂商”配置自己的 API Key。</p>
      {message && <div role="status" className="rounded-ctl border border-line bg-accent-soft p-3 text-xs">{message}</div>}
    </div>
  );
}
