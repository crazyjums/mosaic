// ChatGPT 剩余额度的显示规则（状态栏和 ChatGPT 账号页共用）。纯函数，方便测试。

import type { QuotaWindow } from "../bridge";
import { t } from "../i18n";

/** 窗口名称：5 小时 / 每周，其他长度按小时或天显示。 */
export function windowLabel(seconds: number): string {
  if (seconds > 0 && Math.abs(seconds - 5 * 3600) < 600) return t("5 小时用量上限");
  if (seconds > 0 && Math.abs(seconds - 7 * 86400) < 3600) return t("每周限额");
  if (seconds >= 86400) return t("{n} 天限额", { n: Math.round(seconds / 86400) });
  if (seconds > 0) return t("{n} 小时限额", { n: Math.round(seconds / 3600) });
  return t("用量限额");
}

/** 状态栏里的短名称。 */
export function windowShort(seconds: number): string {
  if (seconds > 0 && Math.abs(seconds - 5 * 3600) < 600) return "5h";
  if (seconds > 0 && Math.abs(seconds - 7 * 86400) < 3600) return t("周");
  if (seconds >= 86400) return `${Math.round(seconds / 86400)}d`;
  return `${Math.round(seconds / 3600)}h`;
}

/** 剩余百分比（整数）。 */
export const remaining = (w: QuotaWindow) => Math.max(0, Math.min(100, Math.round(100 - w.used_percent)));

/** 剩余额度的颜色：低于 20% 红色，低于 50% 黄色。 */
export function remainingTone(pct: number): "danger" | "warn" | "ok" {
  return pct < 20 ? "danger" : pct < 50 ? "warn" : "ok";
}

/** 重置时间：一天以内显示"x 小时 y 分钟后重置"，更久显示日期和时间。 */
export function fmtReset(resetAt: string | undefined, now = Date.now()): string {
  if (!resetAt) return "";
  const at = Date.parse(resetAt);
  if (!Number.isFinite(at)) return "";
  const ms = at - now;
  if (ms <= 0) return t("即将重置");
  const min = Math.round(ms / 60000);
  if (min < 60) return t("{m} 分钟后重置", { m: Math.max(1, min) });
  if (min < 24 * 60) return t("{h} 小时 {m} 分钟后重置", { h: Math.floor(min / 60), m: min % 60 });
  const d = new Date(at);
  return t("{date} 重置", { date: d.toLocaleString(undefined, { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }) });
}

/** 套餐名：plus → Plus。 */
export const planName = (plan?: string) => (plan ? plan.charAt(0).toUpperCase() + plan.slice(1) : "");
