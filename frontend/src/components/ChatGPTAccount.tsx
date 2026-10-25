import { useEffect, useRef, useState } from "react";
import { backend, type ChatGPTStatus, type ChatGPTUsage } from "../bridge";
import { fmtTokens } from "../store/metrics";
import { t } from "../i18n";
import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";

// 设置 → 模型厂商 → ChatGPT 账号：用 ChatGPT 账号登录，使用套餐里的额度。
//
// 登录在系统浏览器里完成（OpenAI 的 Sign in with ChatGPT），后端在本机 127.0.0.1 上等待回调。
// 这里只负责发起、轮询状态和展示结果；令牌从不经过前端。

const errMsg = (e: unknown) => String(e instanceof Error ? e.message : e);

export default function ChatGPTAccount({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [st, setSt] = useState<ChatGPTStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const reload = useSettings((s) => s.reload);
  const refreshMeta = useChat((s) => s.refreshMeta);
  const prevState = useRef<ChatGPTStatus["state"] | null>(null);

  const load = async () => {
    try {
      const next = await backend().getChatGPT();
      setSt(next);
      // 登录完成：刷新厂商列表（多了 chatgpt）和聊天界面（可能自动切换到了 chatgpt）。
      if (prevState.current === "pending" && next.state === "logged_in") {
        await Promise.all([reload(), refreshMeta()]);
        onLoggedIn();
      }
      prevState.current = next.state;
    } catch (e) {
      setError(errMsg(e));
    }
  };

  useEffect(() => void load(), []); // 挂载时读取一次；等待登录期间的轮询见下
  // 等待登录期间每秒查询一次状态。
  useEffect(() => {
    if (st?.state !== "pending") return;
    const timer = setInterval(() => void load(), 1000);
    return () => clearInterval(timer);
  });

  if (!st) return <div className="p-6 text-sm text-muted">{t("加载中…")}</div>;

  const login = async (newAccount: boolean) => {
    setBusy(true);
    setError("");
    try {
      await backend().loginChatGPT(newAccount);
      await load();
    } catch (e) {
      setError(errMsg(e));
    } finally {
      setBusy(false);
    }
  };
  const logout = async () => {
    if (!confirm(t("退出 ChatGPT 账号？本机保存的令牌会被删除并吊销。"))) return;
    setBusy(true);
    try {
      await backend().logoutChatGPT();
    } catch (e) {
      setError(errMsg(e)); // 吊销失败时本地也已经退出，只是提示一下
    } finally {
      setBusy(false);
      await Promise.all([load(), reload(), refreshMeta()]);
    }
  };
  const copy = async () => {
    if (!st.login_url) return;
    try {
      await navigator.clipboard.writeText(st.login_url);
      setCopied(true);
    } catch {
      // 剪贴板不可用时用户可以手动选中复制
    }
  };

  return (
    <div className="space-y-3 rounded-lg border border-line bg-panel-2 px-4 py-3 text-sm">
      <div className="flex items-center gap-2">
        <span
          className={`h-2 w-2 rounded-full ${st.state === "logged_in" ? "bg-ok" : st.state === "pending" ? "animate-pulse bg-warn" : "bg-muted/50"}`}
        />
        <span className="font-medium">
          {st.state === "logged_in"
            ? t("已登录 {email}", { email: st.email || "ChatGPT" })
            : st.state === "pending"
              ? t("等待你在浏览器中完成登录…")
              : t("未登录")}
        </span>
        {st.state === "logged_in" && st.plan && <span className="rounded bg-accent/15 px-1.5 text-[11px] text-accent">{st.plan}</span>}
        <div className="flex-1" />
        {st.state === "logged_out" && (
          <>
            {st.registered && (
              <button className="btn" onClick={() => void login(true)} disabled={busy}>
                {t("使用其他账号")}
              </button>
            )}
            <button className="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-on-accent hover:opacity-90 disabled:opacity-50" onClick={() => void login(false)} disabled={busy}>
              {t("用 ChatGPT 账号登录")}
            </button>
          </>
        )}
        {st.state === "pending" && (
          <button className="btn" onClick={() => void backend().cancelChatGPTLogin().then(load)}>
            {t("取消")}
          </button>
        )}
        {st.state === "logged_in" && (
          <button className="rounded-md px-3 py-1.5 text-sm text-danger hover:bg-danger/10" onClick={() => void logout()} disabled={busy}>
            {t("退出登录")}
          </button>
        )}
      </div>

      {st.state === "pending" && st.login_url && (
        <div className="text-xs text-muted">
          {t("浏览器没有自动打开？复制下面的地址，在这台电脑的浏览器中打开：")}
          <div className="mt-1 flex gap-2">
            <input className="input flex-1 font-mono text-[11px]" readOnly value={st.login_url} onFocus={(e) => e.target.select()} />
            <button className="btn" onClick={() => void copy()}>
              {copied ? t("已复制") : t("复制")}
            </button>
          </div>
        </div>
      )}

      {st.state === "logged_out" && (
        <p className="text-xs leading-relaxed text-muted">
          {t("使用 OpenAI 官方的“Sign in with ChatGPT”：在浏览器中登录并授权后，可以用 ChatGPT 套餐（Go / Plus / Pro 等）里的额度调用 GPT 模型，不需要 API Key。用量计入你的 ChatGPT 额度，可以在 ChatGPT 的设置中为本应用设置上限或撤销授权。")}
        </p>
      )}
      {st.state === "logged_in" && (
        <p className="text-xs leading-relaxed text-muted">
          {t("令牌只保存在本机的数据目录中，不会发给界面或其他程序。下面的模型列表可以编辑；额度用完时会提示错误，不会自动改用付费 API。")}
        </p>
      )}
      {st.state === "logged_in" && <UsageSummary />}
      {st.state === "logged_in" && st.models_error && <div className="text-xs text-warn">{st.models_error}</div>}
      {(error || st.error) && <div className="text-xs text-danger">{error || st.error}</div>}
    </div>
  );
}

// 本应用用掉的套餐额度。OpenAI 没有提供查询剩余额度的接口，剩余额度只能在 ChatGPT 网页上看。
function UsageSummary() {
  const [u, setU] = useState<ChatGPTUsage | null>(null);
  useEffect(() => {
    void backend().getChatGPTUsage().then(setU).catch(() => {});
  }, []);
  if (!u) return null;
  const total = (w: ChatGPTUsage["last_5h"]) => fmtTokens(w.input_tokens + w.output_tokens);
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-line pt-2 text-xs">
      <span className="text-muted">{t("本应用用量")}</span>
      <span>{t("最近 5 小时 {t}（{n} 次）", { t: total(u.last_5h), n: u.last_5h.requests })}</span>
      <span>{t("最近 7 天 {t}（{n} 次）", { t: total(u.last_7d), n: u.last_7d.requests })}</span>
      {u.limit_hit_at && <span className="text-danger">{t("⚠ GPT 额度已用完")}</span>}
      <div className="flex-1" />
      <button className="text-accent hover:underline" onClick={() => void backend().openChatGPTUsage()}>
        {t("查看剩余额度 ↗")}
      </button>
    </div>
  );
}
