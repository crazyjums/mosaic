// 应用外壳：左侧会话栏 + 右侧对话区（消息流、输入框、状态栏）+ 权限弹窗。
// 组件只从 useChat 读状态、调用动作；事件处理全部在 store/reducer.ts。

import { useEffect, useState } from "react";
import ActivityPanel from "./components/ActivityPanel";
import Composer from "./components/Composer";
import SidePanel, { toggleSideTab, useArtifactCount } from "./components/SidePanel";
import MessageList from "./components/MessageList";
import PermissionDialog from "./components/PermissionDialog";
import SettingsDialog from "./components/SettingsDialog";
import UpdateButton from "./components/UpdateButton";
import GitWindow from "./components/GitWindow";
import UpdateDialog from "./components/UpdateDialog";
import Sidebar from "./components/Sidebar";
import EditableTitle from "./components/EditableTitle";
import StatusBar from "./components/StatusBar";
import { t } from "./i18n";
import { useChat } from "./store/chatStore";
import { useUpdate } from "./store/updateStore";
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
  const sideTab = useUIPrefs((s) => s.prefs.sideTab);
  const artifactCount = useArtifactCount();

  useEffect(() => {
    void loadPrefs();
    void init();
    void useUpdate.getState().init();
  }, [init, loadPrefs]);

  return (
    <div key={lang} className="flex h-full bg-bg text-fg">
      <Sidebar />
      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center border-b border-line px-5 text-sm">
          <HeaderTitle title={title} />
          <div className="flex-1" />
          <UpdateButton />
          {/* 右侧面板（上下文 / 产物）只在宽窗口显示，按钮也一样；点当前展开的标签会折叠它 */}
          <div className="hidden items-center gap-1 lg:flex">
            <button
              className={`rounded-md px-2 py-1 text-xs ${contextPanel && sideTab === "context" ? "bg-panel-2 text-fg" : "text-muted hover:text-fg"}`}
              onClick={() => toggleSideTab("context")}
              title={t("显示 / 隐藏上下文明细")}
              aria-pressed={contextPanel && sideTab === "context"}
            >
              {t("上下文")}
            </button>
            <button
              className={`rounded-md px-2 py-1 text-xs ${contextPanel && sideTab === "artifacts" ? "bg-panel-2 text-fg" : "text-muted hover:text-fg"}`}
              onClick={() => toggleSideTab("artifacts")}
              title={t("查看 agent 写出的文件：网页、流程图、文档")}
              aria-pressed={contextPanel && sideTab === "artifacts"}
            >
              {t("产物")}
              {artifactCount > 0 && <span className="ml-1 rounded-full bg-accent px-1.5 text-[10px] text-on-accent">{artifactCount}</span>}
            </button>
          </div>
        </header>
        {toast && (
          <div className="flex items-center justify-between border-b border-danger/40 bg-danger/10 px-5 py-2 text-sm text-danger">
            <span>{toast}</span>
            <button className="text-xs opacity-70 hover:opacity-100" onClick={dismissToast}>
              {t("关闭")}
            </button>
          </div>
        )}
        {/* 右侧面板从标题栏下面一直到窗口底部（和输入框、状态栏并排），所以把它放在最外层的横向布局里 */}
        <div className="flex min-h-0 flex-1">
          <div className="flex min-w-0 flex-1 flex-col">
            <div className="flex min-h-0 flex-1">
              <MessageList />
              <ActivityPanel />
            </div>
            <Composer />
            <StatusBar />
          </div>
          <SidePanel />
        </div>
      </main>
      <PermissionDialog />
      <UpdateDialog />
      <GitWindow />
      <SettingsDialog />
    </div>
  );
}

// 顶部的会话标题：双击或点 ✎ 改名。空会话（还没发消息）也能先起名，第一轮结束时一起保存。
function HeaderTitle({ title }: { title: string }) {
  const sessionId = useChat((s) => s.sessionId);
  const renameSession = useChat((s) => s.renameSession);
  const [editing, setEditing] = useState(false);
  return (
    <div className="group flex min-w-0 items-center gap-1">
      <EditableTitle value={title} placeholder={t("新会话")} editing={editing} onEditingChange={setEditing} onSave={(v) => (sessionId ? renameSession(sessionId, v) : false)} className="font-medium" />
      {!editing && (
        <button className="shrink-0 rounded px-1 text-xs text-muted opacity-0 hover:text-fg group-hover:opacity-100 focus:opacity-100" onClick={() => setEditing(true)} title={t("改名")} aria-label={t("改名")}>
          ✎
        </button>
      )}
    </div>
  );
}
