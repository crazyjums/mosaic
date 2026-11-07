import { StrictMode, useEffect } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { shellWindowKind } from "./bridge/electron";
import { GitStandalone } from "./components/GitWindow";
import { useUIPrefs } from "./store/uiPrefs";
import "./index.css";

// 壳打开的独立 Git 窗口（?window=git）只渲染 Git 视图；其余情况是完整的应用。
// 独立窗口也要加载界面偏好（主题、语言），否则会和主窗口长得不一样。
function Standalone() {
  const load = useUIPrefs((s) => s.load);
  const lang = useUIPrefs((s) => s.lang);
  useEffect(() => {
    void load();
  }, [load]);
  return <div key={lang} className="h-full"><GitStandalone /></div>;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>{shellWindowKind() === "git" ? <Standalone /> : <App />}</StrictMode>,
);
