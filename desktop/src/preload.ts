// 渲染进程能用的全部后端能力：window.mosaic。
//
// 渲染进程开着 contextIsolation 和 sandbox、没有 Node：它只能通过这里暴露的两个函数和主进程说话，
// 主进程再按白名单转给后端。不要往这里加“通用”的东西（比如执行命令、读文件）。
import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("mosaic", {
  call: (method: string, args: unknown[]): Promise<unknown> => ipcRenderer.invoke("mosaic:call", method, args),
  onEvent: (cb: (data: unknown) => void): (() => void) => {
    const listener = (_e: unknown, data: unknown) => cb(data);
    ipcRenderer.on("mosaic:event", listener);
    return () => ipcRenderer.removeListener("mosaic:event", listener);
  },
  /** 壳要求界面跳转：打开某个会话（点通知）或新建会话并聚焦输入框（全局快捷键）。 */
  onNavigate: (cb: (target: unknown) => void): (() => void) => {
    const listener = (_e: unknown, target: unknown) => cb(target);
    ipcRenderer.on("mosaic:navigate", listener);
    return () => ipcRenderer.removeListener("mosaic:navigate", listener);
  },
  /** 桌面设置（关窗行为、通知、全局快捷键），由壳保存。 */
  getShellSettings: (): Promise<unknown> => ipcRenderer.invoke("mosaic:shell-settings:get"),
  setShellSettings: (patch: unknown): Promise<unknown> => ipcRenderer.invoke("mosaic:shell-settings:set", patch),
  /** 请壳打开独立窗口（目前只有 git）。 */
  openWindow: (kind: string): Promise<void> => ipcRenderer.invoke("mosaic:open-window", kind),
  /** 存网页预览用的令牌（预览 iframe 里没有 preload，拿不到）。 */
  previewToken: ipcRenderer.sendSync("mosaic:preview-token") as string,
  platform: process.platform,
});
