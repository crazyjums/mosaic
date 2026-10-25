import { useState } from "react";
import { backend, type ChatGPTUsage } from "../bridge";
import { t } from "../i18n";
import { fmtTokens } from "../store/metrics";
import { fmtReset, planName, remaining, remainingTone, windowLabel } from "../store/quota";

// ChatGPT 额度的详细信息：剩余额度（5 小时 / 每周，开启后才有）、额外额度、本应用用量，以及开关。
// 状态栏的弹出卡片和设置里的 ChatGPT 账号页共用这个组件。

const TONE_TEXT = { ok: "text-ok", warn: "text-warn", danger: "text-danger" } as const;
const TONE_BG = { ok: "bg-ok", warn: "bg-warn", danger: "bg-danger" } as const;

export default function ChatGPTQuotaDetails({ usage: u, onChange }: { usage: ChatGPTUsage; onChange: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const windows = u.quota?.windows ?? [];
  const toggle = async () => {
    setBusy(true);
    setError("");
    try {
      await backend().setCodexQuota(!u.quota_enabled);
      onChange();
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };
  const appTotal = (w: ChatGPTUsage["last_5h"]) => fmtTokens(w.input_tokens + w.output_tokens);

  return (
    <div className="space-y-2 text-xs">
      {u.quota && (
        <>
          {u.quota.plan && <div className="text-muted">{t("额度来源：你的 {plan} 套餐", { plan: planName(u.quota.plan) })}</div>}
          {windows.map((w) => {
            const pct = remaining(w);
            const tone = remainingTone(pct);
            return (
              <div key={w.seconds}>
                <div className="flex items-baseline">
                  <span className="text-fg">{windowLabel(w.seconds)}</span>
                  <div className="flex-1" />
                  <span className={`font-medium tabular-nums ${TONE_TEXT[tone]}`}>{t("剩余 {p}%", { p: pct })}</span>
                </div>
                <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-line">
                  <div className={`h-full ${TONE_BG[tone]}`} style={{ width: `${Math.max(pct, 1)}%` }} />
                </div>
                {w.reset_at && <div className="mt-0.5 text-[10px] text-muted">{fmtReset(w.reset_at)}</div>}
              </div>
            );
          })}
          {u.quota.credits !== undefined && (
            <div className="flex">
              <span className="text-fg">{t("额外额度")}</span>
              <div className="flex-1" />
              <span className="tabular-nums">{u.quota.credits}</span>
            </div>
          )}
        </>
      )}
      {u.quota_enabled && u.quota_error && <div className="text-warn">{u.quota_error}</div>}
      {u.limit_hit_at && <div className="text-danger">{t("⚠ GPT 额度已用完")}</div>}

      <div className="border-t border-line pt-2 text-muted">
        {t("本应用用量：最近 5 小时 {a}（{n} 次）· 最近 7 天 {b}（{m} 次）", {
          a: appTotal(u.last_5h),
          n: u.last_5h.requests,
          b: appTotal(u.last_7d),
          m: u.last_7d.requests,
        })}
      </div>

      <label className="flex cursor-pointer items-start gap-2 border-t border-line pt-2">
        <input type="checkbox" className="mt-0.5 accent-[var(--accent)]" checked={u.quota_enabled} onChange={() => void toggle()} disabled={busy} />
        <span>
          <span className="text-fg">{t("显示剩余额度（读取本机 Codex 登录）")}</span>
          <span className="mt-0.5 block text-[10px] leading-relaxed text-muted">
            {t("Sign in with ChatGPT 目前查不到剩余额度。开启后读取官方 Codex CLI 的登录（~/.codex/auth.json），只读地查询 ChatGPT 的用量接口；不会修改 Codex 的登录。这是未公开的接口，可能随时失效。")}
          </span>
        </span>
      </label>
      {error && <div className="text-danger">{error}</div>}

      <button className="text-accent hover:underline" onClick={() => void backend().openChatGPTUsage()}>
        {t("在 ChatGPT 中查看用量 ↗")}
      </button>
    </div>
  );
}
