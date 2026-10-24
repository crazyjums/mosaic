import { useSettings } from "../store/settingsStore";
import { ITEMS, useStatusLine, type StatusLineConfig } from "../store/statusline";
import StatusBar from "./StatusBar";

// 设置 → 状态栏：勾选要显示的项、调整顺序、少量显示选项。顶部是实时预览（就是真正的状态栏组件）。
// 每次修改立即保存（settings.json 的 statusline 字段），不需要点"保存"。

export default function StatusLineSettings() {
  const { config, update, reset } = useStatusLine();
  const setTab = useSettings((s) => s.setTab);

  const toggle = (i: number) =>
    update((c) => ({ ...c, items: c.items.map((it, j) => (j === i ? { ...it, enabled: !it.enabled } : it)) }));

  const move = (i: number, delta: -1 | 1) =>
    update((c) => {
      const j = i + delta;
      if (j < 0 || j >= c.items.length) return c;
      const items = [...c.items];
      [items[i], items[j]] = [items[j], items[i]];
      return { ...c, items };
    });

  const setOption = <K extends keyof StatusLineConfig>(key: K, value: StatusLineConfig[K]) => update((c) => ({ ...c, [key]: value }));

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="shrink-0 border-b border-line px-5 py-4">
        <div className="mb-2 text-xs font-medium text-muted">预览</div>
        <div className="rounded-lg border border-line bg-bg">
          <StatusBar />
        </div>
        <p className="mt-2 text-[11px] text-muted">
          某一项暂时没有数据时会自动隐藏，例如还没发过消息时不显示上下文和耗时、工作区不是 git 仓库时不显示分支。
        </p>
      </div>

      <div className="flex-1 overflow-y-auto px-5 py-4">
        <div className="mb-2 text-xs font-medium text-muted">显示的项目（从左到右）</div>
        <ul className="divide-y divide-line rounded-lg border border-line">
          {config.items.map((it, i) => (
            <li key={it.id} className="flex items-center gap-3 px-3 py-2.5">
              <input type="checkbox" className="h-4 w-4 accent-[var(--accent)]" checked={it.enabled} onChange={() => toggle(i)} id={`sl-${it.id}`} />
              <label htmlFor={`sl-${it.id}`} className={`min-w-0 flex-1 cursor-pointer ${it.enabled ? "" : "opacity-50"}`}>
                <div className="text-sm">{ITEMS[it.id].label}</div>
                <div className="text-[11px] text-muted">{ITEMS[it.id].description}</div>
              </label>
              <div className="flex gap-1">
                <button className="btn px-2 py-0.5" onClick={() => move(i, -1)} disabled={i === 0} title="左移">
                  ↑
                </button>
                <button className="btn px-2 py-0.5" onClick={() => move(i, 1)} disabled={i === config.items.length - 1} title="右移">
                  ↓
                </button>
              </div>
            </li>
          ))}
        </ul>

        <div className="mt-5 mb-2 text-xs font-medium text-muted">显示方式</div>
        <div className="space-y-3 rounded-lg border border-line px-3 py-3 text-sm">
          <div className="flex items-center gap-3">
            <span className="w-28 shrink-0">工作区</span>
            {(["name", "full"] as const).map((v) => (
              <label key={v} className="flex cursor-pointer items-center gap-1.5">
                <input type="radio" className="accent-[var(--accent)]" checked={config.workspaceStyle === v} onChange={() => setOption("workspaceStyle", v)} />
                {v === "name" ? "只显示目录名" : "完整路径"}
              </label>
            ))}
          </div>
          <label className="flex cursor-pointer items-center gap-3">
            <span className="w-28 shrink-0">Token 用量</span>
            <input type="checkbox" className="accent-[var(--accent)]" checked={config.showCache} onChange={(e) => setOption("showCache", e.target.checked)} />
            显示缓存命中的 token 数
          </label>
        </div>

        <div className="mt-5 rounded-lg border border-line bg-panel-2 px-3 py-2.5 text-xs text-muted">
          “上下文占用”的百分比和“预估花费”需要知道每个模型的上下文窗口和单价，
          请在
          <button className="mx-1 text-accent hover:underline" onClick={() => setTab("providers")}>
            模型厂商
          </button>
          → 编辑厂商 → 高级选项 中填写。
        </div>
      </div>

      <footer className="flex shrink-0 items-center border-t border-line px-5 py-3">
        <span className="text-xs text-muted">修改会立即保存</span>
        <div className="flex-1" />
        <button className="btn" onClick={() => confirm("恢复状态栏的默认设置？") && void reset()}>
          恢复默认
        </button>
      </footer>
    </div>
  );
}
