import { useEffect, useState } from "react";
import type { PluginPreview } from "../bridge";
import { t } from "../i18n";

// 安装确认：把这个插件要做的事逐项摆出来，用户确认后才真正安装。
//
// 插件是用户电脑上的外部程序，应用无法沙箱它，所以这里的目标是让用户在知情的前提下授予信任：
//   - 要启动的命令（原样显示）；
//   - 每个 MCP 服务作者声明的风险等级——声明为只读的，要用户单独勾选才按只读运行，不勾选一律每次询问；
//   - 依赖的外部命令、声明会访问的域名（只用于确认，应用不会拦截）、需要填的配置、带来的技能和命令。
export default function PluginInstallDialog({ preview, busy, error, onConfirm, onCancel }: { preview: PluginPreview; busy: boolean; error: string; onConfirm: (approved: string[]) => void; onCancel: () => void }) {
  const p = preview.plugin;
  const [approved, setApproved] = useState<string[]>([]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && !busy && onCancel();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [busy, onCancel]);
  const toggle = (name: string) => setApproved((a) => (a.includes(name) ? a.filter((x) => x !== name) : [...a, name]));
  const missing = p.requires.filter((r) => !r.found);
  const sensitive = p.config.some((c) => c.type === "secret");

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-6" role="dialog" aria-modal="true" aria-label={t("安装插件")}>
      <div className="flex max-h-[88vh] w-full max-w-2xl flex-col rounded-modal border border-line bg-panel shadow-pop">
        <div className="border-b border-line px-5 py-4">
          <div className="text-[15px] font-semibold">
            {preview.existing ? t("更新插件") : t("安装插件")}：{p.name} <span className="font-normal text-muted">v{p.version}</span>
          </div>
          {p.description && <div className="mt-1 text-sm text-muted">{p.description}</div>}
          {preview.existing && (
            <div className="mt-2 rounded-ctl bg-accent-soft px-3 py-1.5 text-xs">
              {t("已安装 v{v}，这次会覆盖更新：配置会保留，但下面的风险确认需要重新勾选。", { v: preview.existing.version })}
            </div>
          )}
        </div>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4 text-sm">
          <div className="rounded-ctl border border-warn/40 bg-warn/10 px-3 py-2 text-xs leading-relaxed text-warn">
            {t("插件是在你电脑上运行的外部程序，应用无法限制它能做什么。请只安装你信任的作者写的插件，并仔细看下面它要运行的内容。")}
          </div>

          <Section title={t("要运行的程序")} empty={t("这个插件不会启动任何程序")} show={p.servers.length > 0}>
            {p.servers.map((s) => (
              <div key={s.name} className="rounded-ctl border border-line p-2.5">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs font-medium">{s.name}</span>
                  <span className="rounded-full border border-line px-1.5 text-[10px] text-muted">{s.transport}</span>
                </div>
                <pre className="mt-1.5 overflow-x-auto whitespace-pre-wrap break-all rounded-sm bg-code px-2 py-1.5 font-mono text-[11px]">{s.command}</pre>
                {s.sensitive && <div className="mt-2 text-xs text-warn">{t("它读取的是私密内容，每次调用都会询问你，不能设为“总是允许”")}</div>}
                {s.declared_risk !== "exec" && !s.sensitive ? (
                  <label className="mt-2 flex cursor-pointer items-start gap-2 text-xs">
                    <input type="checkbox" className="mt-0.5" checked={approved.includes(s.name)} onChange={() => toggle(s.name)} />
                    <span>
                      {t("作者声明它的工具是“{risk}”。允许按这个等级运行（不再每次询问）", { risk: s.declared_risk === "read" ? t("只读") : t("写入") })}
                      <span className="block text-muted">{t("不勾选：它的每个工具调用都会先询问你。")}</span>
                    </span>
                  </label>
                ) : (
                  !s.sensitive && <div className="mt-2 text-xs text-muted">{t("它的工具每次调用都会先询问你。")}</div>
                )}
              </div>
            ))}
          </Section>

          <Section title={t("带来的技能和命令")} empty="" show={p.skills.length + p.commands.length > 0}>
            <div className="flex flex-wrap gap-1.5">
              {p.skills.map((s) => (
                <span key={"s" + s} className="rounded-full bg-panel-2 px-2 py-0.5 text-xs">
                  {t("技能")} · {s}
                </span>
              ))}
              {p.commands.map((c) => (
                <span key={"c" + c.name} className="rounded-full bg-panel-2 px-2 py-0.5 font-mono text-xs" title={c.description}>
                  /{c.name}
                </span>
              ))}
            </div>
          </Section>

          <Section title={t("依赖的外部命令")} empty="" show={p.requires.length > 0}>
            <ul className="space-y-1 text-xs">
              {p.requires.map((r) => (
                <li key={r.command} className="flex items-center gap-2">
                  <span className={r.found ? "text-ok" : "text-warn"}>{r.found ? "✓" : "✗"}</span>
                  <span className="font-mono">{r.command}</span>
                  <span className="text-muted">{r.found ? t("已找到") : t("没有找到：安装后相关功能不能用，直到你装好它")}</span>
                </li>
              ))}
            </ul>
            {missing.length > 0 && <div className="mt-1 text-xs text-muted">{t("缺少依赖不影响安装，之后装好即可。")}</div>}
          </Section>

          <Section title={t("声明会访问的网络")} empty="" show={p.network.length > 0}>
            <div className="flex flex-wrap gap-1.5">
              {p.network.map((h) => (
                <span key={h} className="rounded-full bg-panel-2 px-2 py-0.5 font-mono text-xs">
                  {h}
                </span>
              ))}
            </div>
            <div className="mt-1 text-xs text-muted">{t("这只是作者的声明，用来让你判断是否放心；应用不会拦截它的网络访问。")}</div>
          </Section>

          <Section title={t("需要你填写的设置")} empty="" show={p.config.length > 0}>
            <ul className="space-y-0.5 text-xs">
              {p.config.map((c) => (
                <li key={c.key}>
                  {c.label}
                  {c.required && <span className="text-danger"> *</span>}
                  {c.type === "secret" && <span className="ml-1.5 rounded-full bg-panel-2 px-1.5 text-[10px] text-muted">{t("密钥")}</span>}
                </li>
              ))}
            </ul>
            {sensitive && <div className="mt-1 text-xs text-muted">{t("密钥只保存在系统钥匙串里，不会写进文件，也不会显示在界面上。")}</div>}
          </Section>

          <div className="text-xs text-muted">{t("插件读取到的内容（例如文档、消息）会作为工具结果发给你当前选择的模型厂商。")}</div>
        </div>

        {error && <div className="border-t border-danger/40 bg-danger/10 px-5 py-2 text-sm text-danger">{error}</div>}
        <div className="flex justify-end gap-2 border-t border-line px-5 py-3">
          <button className="btn" onClick={onCancel} disabled={busy}>
            {t("取消")}
          </button>
          <button className="btn-primary" onClick={() => onConfirm(approved)} disabled={busy}>
            {busy ? t("安装中…") : preview.existing ? t("更新并启用") : t("安装并启用")}
          </button>
        </div>
      </div>
    </div>
  );
}

function Section({ title, show, empty, children }: { title: string; show: boolean; empty: string; children: React.ReactNode }) {
  if (!show) return empty ? <div className="text-xs text-muted">{empty}</div> : null;
  return (
    <section>
      <h3 className="mb-1.5 text-xs font-medium text-muted">{title}</h3>
      {children}
    </section>
  );
}
