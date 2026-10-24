// 应用外壳：左侧会话栏 + 右侧对话区（消息流、输入框、状态栏）+ 权限弹窗。
// 组件只从 useChat 读状态、调用动作；事件处理全部在 store/reducer.ts。

import { useEffect } from "react";
import ActivityPanel from "./components/ActivityPanel";
import Composer from "./components/Composer";
import MessageList from "./components/MessageList";
import PermissionDialog from "./components/PermissionDialog";
import SettingsDialog from "./components/SettingsDialog";
import Sidebar from "./components/Sidebar";
import StatusBar from "./components/StatusBar";
import { useChat } from "./store/chatStore";

export default function App() {
  const init = useChat((s) => s.init);
  const toast = useChat((s) => s.toast);
  const dismissToast = useChat((s) => s.dismissToast);
  const title = useChat((s) => s.title);

  useEffect(() => {
    void init();
  }, [init]);

  return (
    <div className="flex h-full bg-bg text-fg">
      <Sidebar />
      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center border-b border-line px-5 text-sm">
          <span className="truncate font-medium">{title || "新会话"}</span>
        </header>
        {toast && (
          <div className="flex items-center justify-between border-b border-danger/40 bg-danger/10 px-5 py-2 text-sm text-danger">
            <span>{toast}</span>
            <button className="text-xs opacity-70 hover:opacity-100" onClick={dismissToast}>
              关闭
            </button>
          </div>
        )}
        <div className="flex min-h-0 flex-1">
          <MessageList />
          <ActivityPanel />
        </div>
        <Composer />
        <StatusBar />
      </main>
      <PermissionDialog />
      <SettingsDialog />
    </div>
  );
}
