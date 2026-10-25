import { useEffect, useState, type ReactNode } from "react";
import { backend, type UsageReport } from "../bridge";
import { t } from "../i18n";
import { fmtTokens } from "../store/metrics";
import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";
import { Notice } from "./GeneralSettings";

// 设置 → 数据：本地数据存在哪里、占多大，打开目录，清空历史。

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

export default function DataSettings() {
  const { dataInfo, openDataDir, clearSessions } = useSettings();
  const running = useChat((s) => s.running);
  const [cleared, setCleared] = useState<number | null>(null);

  if (!dataInfo) return <div className="p-6 text-sm text-muted">加载中…</div>;

  const clear = async () => {
    if (!confirm(`删除全部 ${dataInfo.sessions} 个历史会话（包括当前会话）？此操作不能撤销。`)) return;
    setCleared(await clearSessions());
  };

  return (
    <div className="flex-1 space-y-6 overflow-y-auto px-6 py-5">
      <section>
        <div className="mb-2 text-sm font-medium">数据目录</div>
        <div className="divide-y divide-line rounded-lg border border-line text-sm">
          <Item label="目录" value={dataInfo.data_dir} />
          <Item label="设置文件" value={dataInfo.settings_path} note="界面上的所有设置，包括 API Key（明文，权限 600）" />
          <Item label="会话数据库" value={dataInfo.db_path} note={`${dataInfo.sessions} 个会话 · ${fmtBytes(dataInfo.db_bytes)}`} />
        </div>
        <div className="mt-3 flex items-center gap-3">
          <button className="btn" onClick={() => void openDataDir()}>
            在文件管理器中打开
          </button>
          <span className="text-[11px] text-muted">数据目录本身只能在 config.toml 的 data_dir 中修改（设置文件就存放在这个目录里）。</span>
        </div>
      </section>

      <UsageSummary />

      <section>
        <div className="mb-2 text-sm font-medium">历史会话</div>
        <Notice tone="info">
          清空后会删除所有保存的会话并回收磁盘空间。厂商配置、状态栏等设置不受影响。
        </Notice>
        <div className="mt-3 flex items-center gap-3">
          <button
            className="rounded-md border border-danger/50 px-3 py-1.5 text-sm text-danger hover:bg-danger/10 disabled:opacity-50"
            onClick={clear}
            disabled={running || dataInfo.sessions === 0}
          >
            清空全部历史会话
          </button>
          {running && <span className="text-xs text-warn">agent 正在运行，本轮结束后才能清空</span>}
          {cleared !== null && <span className="text-xs text-ok">✓ 已删除 {cleared} 个会话</span>}
        </div>
      </section>
    </div>
  );
}

function Item({ label, value, note }: { label: string; value: string; note?: ReactNode }) {
  return (
    <div className="flex gap-4 px-3 py-2.5">
      <span className="w-20 shrink-0 text-muted">{label}</span>
      <span className="min-w-0 flex-1">
        <span className="block break-all font-mono text-xs">{value}</span>
        {note && <span className="mt-0.5 block text-[11px] text-muted">{note}</span>}
      </span>
    </div>
  );
}

// 用量的一行汇总，详情在"用量"页（按天展开、花费、导出）。
function UsageSummary() {
  const setTab = useSettings((s) => s.setTab);
  const [all, setAll] = useState<UsageReport | null>(null);
  const [today, setToday] = useState<UsageReport | null>(null);
  useEffect(() => {
    void backend().getUsageReport({ range: "all", by: "day" }).then(setAll).catch(() => {});
    void backend().getUsageReport({ range: "today", by: "day" }).then(setToday).catch(() => {});
  }, []);
  if (!all) return null;
  const sum = (r: UsageReport | null) => (r ? fmtTokens(r.totals.input + r.totals.output) : "—");
  return (
    <section>
      <div className="mb-2 text-sm font-medium">{t("用量")}</div>
      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-line px-3 py-2 text-sm">
        <span>
          {t("今天")} <b className="tabular-nums">{sum(today)}</b> tokens
        </span>
        <span className="text-muted">·</span>
        <span>
          {t("累计")} <b className="tabular-nums">{sum(all)}</b> tokens · {all.totals.requests} {t("请求")}
        </span>
        <div className="flex-1" />
        <button className="text-accent hover:underline" onClick={() => setTab("usage")}>
          {t("查看按天的明细")} →
        </button>
      </div>
    </section>
  );
}
