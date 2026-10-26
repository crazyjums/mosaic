import { backend } from "../bridge";
import { t } from "../i18n";
import { absPath } from "../store/artifacts";
import { useChat } from "../store/chatStore";

/** 用系统默认程序打开工作区里的文件（.html 在默认浏览器里打开）；失败时在界面顶部提示原因。 */
export async function openFileWithToast(path: string): Promise<void> {
  try {
    await backend().openWorkspaceFile(path);
  } catch (e) {
    useChat.setState({ toast: String(e instanceof Error ? e.message : e) });
  }
}

/**
 * 聊天里的文件路径：点击用默认程序打开，悬停显示它在电脑上的绝对路径。
 * 用 <button> 而不是 <a>：它不是网址，WebView 不应该导航。
 */
export function FileLink({ path, label }: { path: string; label?: string }) {
  const workspace = useChat((s) => s.workspace);
  const abs = absPath(workspace, path);
  return (
    <button
      type="button"
      className="inline max-w-full cursor-pointer break-all rounded-sm border-0 bg-transparent p-0 text-left font-mono text-[0.9em] text-accent underline decoration-dotted underline-offset-2 hover:decoration-solid focus-visible:outline-2 focus-visible:outline-accent"
      title={`${abs}\n${t("点击用默认程序打开（网页会在默认浏览器里打开）")}`}
      onClick={() => void openFileWithToast(path)}
    >
      {label ?? path}
    </button>
  );
}
