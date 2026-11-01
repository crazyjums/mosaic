import { backend } from "../bridge";
import { t } from "../i18n";
import { absPath } from "../store/artifacts";
import { useChat } from "../store/chatStore";

/** 安全打开工作区文件：文档使用默认程序，脚本使用明确的文本编辑器，二进制只定位。 */
export async function openFileWithToast(path: string): Promise<void> {
  try {
    await backend().openWorkspaceFile(path);
  } catch (e) {
    useChat.setState({ toast: String(e instanceof Error ? e.message : e) });
  }
}

/** 用户主动在文件管理器中定位任意产物；不执行文件。 */
export async function revealFileWithToast(path: string): Promise<void> {
  try {
    await backend().revealWorkspaceFile(path);
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
      title={`${abs}\n${t("安全打开：脚本将使用文本编辑器，其他文件使用合适的程序")}`}
      onClick={() => void openFileWithToast(path)}
    >
      {label ?? path}
    </button>
  );
}
