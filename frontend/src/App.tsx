// 应用外壳：左侧会话栏 + 右侧对话区（消息流、输入框、状态栏）+ 权限弹窗。
// 组件只从 useChat 读状态、调用动作；事件处理全部在 store/reducer.ts。

import { useEffect } from "react";
import ActivityPanel from "./components/ActivityPanel";
import Composer from "./components/Composer";
import ContextPanel from "./components/ContextPanel";
import MessageList from "./components/MessageList";
import PermissionDialog from "./components/PermissionDialog";
import SettingsDialog from "./components/SettingsDialog";
import Sidebar from "./components/Sidebar";
import StatusBar from "./components/StatusBar";
import { t } from "./i18n";
import { useChat } from "./store/chatStore";
import { useUIPrefs } from "./store/uiPrefs";

export default function App() {
  const init = useChat((s) => s.init);
  const toast = useChat((s) => s.toast);
  const dismissToast = useChat((s) => s.dismissToast);
  const title = useChat((s) => s.title);
  const loadPrefs = useUIPrefs((s) => s.load);
  // 切换语言时用 key 重新挂载整个界面，让所有 t() 重新求值（状态都在 store 里，不会丢）。
  const lang = useUIPrefs((s) => s.lang);
  const contextPanel = useUIPrefs((s) => s.prefs.contextPanel);
  const updatePrefs = useUIPrefs((s) => s.update);

  useEffect(() => {
    void loadPrefs();
    void init();
  }, [init, loadPrefs]);

  return (
    <div key={lang} className="flex h-full bg-bg text-fg">
      <Sidebar />
      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center border-b border-line px-5 text-sm">
          <span className="truncate font-medium">{title || t("新会话")}</span>
          <div className="flex-1" />
          {/* 上下文面板只在宽窗口（xl）显示，按钮也一样 */}
          <button
            className={`hidden rounded-md px-2 py-1 text-xs xl:block ${contextPanel ? "bg-panel-2 text-fg" : "text-muted hover:text-fg"}`}
            onClick={() => void updatePrefs({ contextPanel: !contextPanel })}
            title={t("显示 / 隐藏上下文明细")}
            aria-pressed={contextPanel}
          >
            {t("上下文")}
          </button>
        </header>
        {toast && (
          <div className="flex items-center justify-between border-b border-danger/40 bg-danger/10 px-5 py-2 text-sm text-danger">
            <span>{toast}</span>
            <button className="text-xs opacity-70 hover:opacity-100" onClick={dismissToast}>
              {t("关闭")}
            </button>
          </div>
        )}
        <div className="flex min-h-0 flex-1">
          <MessageList />
          <ActivityPanel />
          <ContextPanel />
        </div>
        <Composer />
        <StatusBar />
      </main>
      <PermissionDialog />
      <SettingsDialog />
    </div>
  );
}
