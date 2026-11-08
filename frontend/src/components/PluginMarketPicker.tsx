import { useEffect } from "react";
import type { MarketInfo } from "../bridge";
import { t } from "../i18n";

// 从 marketplace（一个仓库 / 文件夹里放着多个插件）里挑一个插件。
//
// 选之前就把来源写清楚（路径或仓库）；不能装的条目灰显并写明原因，不是悄悄不见——用户才知道是“这个插件不支持”
// 而不是“列表里没有”。选中后进入和其他来源完全相同的安装确认。
export default function PluginMarketPicker({ market, busy, error, onPick, onClose }: { market: MarketInfo; busy: string; error: string; onPick: (name: string) => void; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && !busy && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [busy, onClose]);
  const usable = market.plugins.filter((p) => p.supported).length;
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50 p-6" role="dialog" aria-modal="true" aria-label={t("选择插件")}>
      <div className="flex max-h-[88vh] w-full max-w-2xl flex-col rounded-modal border border-line bg-panel shadow-pop">
        <div className="border-b border-line px-5 py-4">
          <div className="text-[15px] font-semibold">
            {t("这是一个插件合集")}：{market.name}
          </div>
          <div className="mt-1 text-xs text-muted">
            {market.owner ? t("维护者 {name}", { name: market.owner }) + " · " : ""}
            {t("共 {n} 个插件，{m} 个可以安装", { n: String(market.plugins.length), m: String(usable) })}
          </div>
          {market.description && <div className="mt-1 text-sm text-muted">{market.description}</div>}
          <div className="mt-1 break-all font-mono text-[11px] text-muted">{market.source}</div>
        </div>
        <ul className="min-h-0 flex-1 space-y-2 overflow-y-auto px-5 py-4">
          {market.plugins.map((p) => (
            <li key={p.name} className={`flex items-start gap-3 rounded-ctl border border-line p-3 ${p.supported ? "" : "opacity-60"}`}>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-2">
                  <span className="font-medium">{p.name}</span>
                  {p.category && <span className="rounded-full bg-panel-2 px-2 py-0.5 text-[11px] text-muted">{p.category}</span>}
                </div>
                {p.description && <div className="mt-0.5 text-sm text-muted">{p.description}</div>}
                <div className="mt-1 break-all font-mono text-[11px] text-muted">
                  {p.kind} · {p.where}
                </div>
                {!p.supported && <div className="mt-1 text-xs text-warn">{t("暂时不能安装")}：{p.reason}</div>}
              </div>
              <button className="btn-primary shrink-0 px-3 text-xs" disabled={!p.supported || busy !== ""} onClick={() => onPick(p.name)}>
                {busy === p.name ? t("下载中…") : t("选择")}
              </button>
            </li>
          ))}
        </ul>
        {error && <div className="mx-5 mb-3 rounded-ctl border border-danger/40 bg-danger/10 px-3 py-2 text-sm text-danger">{error}</div>}
        <div className="flex justify-end border-t border-line px-5 py-3">
          <button className="btn px-4 text-sm" onClick={onClose} disabled={busy !== ""}>
            {t("关闭")}
          </button>
        </div>
      </div>
    </div>
  );
}
