// 选择桥接实现：在 Wails 窗口里用真实后端，否则用浏览器演示后端。

import { createDemoBackend } from "./demo";
import type { Backend } from "./types";
import { createWailsBackend, isWails } from "./wails";

export type { Backend, ModelMeta, PermissionMode, ProfileInfo, ProviderInput, ProviderView, SessionSummary, Status, TestResult, WorkspaceInfo } from "./types";

let instance: Backend | null = null;

export function backend(): Backend {
  if (!instance) instance = isWails() ? createWailsBackend() : createDemoBackend();
  return instance;
}
