// 输入框的快捷键规则（设置 → 外观 → 发送快捷键）。单独成文件，方便测试。

/** macOS 上显示 ⌘，其他系统显示 Ctrl。 */
export const MOD_KEY = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘" : "Ctrl";

/**
 * 判断一次按键是否应该发送消息。sendKey 由 设置 → 外观 → 发送快捷键 决定：
 *   - enter：Enter 发送，Shift+Enter 换行；
 *   - mod_enter：Ctrl/⌘+Enter 发送，Enter 换行（适合经常输入多行内容）。
 * 中文输入法选词时按下的 Enter（isComposing）永远不发送。
 */
export function isSendKey(e: { key: string; shiftKey: boolean; ctrlKey: boolean; metaKey: boolean; isComposing: boolean }, sendKey: "enter" | "mod_enter"): boolean {
  if (e.key !== "Enter" || e.isComposing) return false;
  return sendKey === "enter" ? !e.shiftKey && !e.ctrlKey && !e.metaKey : e.ctrlKey || e.metaKey;
}
