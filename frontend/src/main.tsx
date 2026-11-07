import { StrictMode, useEffect } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { shellWindow, type ShellWindow } from "./bridge/electron";
import { ArtifactStandalone } from "./components/ArtifactsPanel";
import { GitStandalone } from "./components/GitWindow";
import { useUIPrefs } from "./store/uiPrefs";
import "./index.css";

// 壳打开的独立窗口（?window=git / ?window=artifact）只渲染对应的视图；其余情况是完整的应用。
// 独立窗口也要加载界面偏好（主题、语言），否则会和主窗口长得不一样。
function Standalone({ win }: { win: ShellWindow }) {
  const load = useUIPrefs((s) => s.load);
  const lang = useUIPrefs((s) => s.lang);
  useEffect(() => {
    void load();
  }, [load]);
  return (
    <div key={lang} className="h-full">
      {win.kind === "git" ? <GitStandalone /> : <ArtifactStandalone path={win.path} workspace={win.workspace} kind={win.artifactKind} />}
    </div>
  );
}

const win = shellWindow();
createRoot(document.getElementById("root")!).render(<StrictMode>{win ? <Standalone win={win} /> : <App />}</StrictMode>);
