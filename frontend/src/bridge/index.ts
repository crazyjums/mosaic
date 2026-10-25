// 选择桥接实现：在 Wails 窗口里用真实后端，否则用浏览器演示后端。

import { createDemoBackend } from "./demo";
import type { Backend } from "./types";
import { createWailsBackend, isWails } from "./wails";

export type {
  Backend, ChatGPTStatus, ChatGPTUsage, Quota, QuotaWindow, ContextBreakdown, ContextItem, ContextPart, DataInfo, General, GeneralView, HistoryPage, LoadedSession, KeychainStatus, MCPServerConfig, MCPServerStatus, ModelMeta, PermissionMode, PermissionRule, ProfileInfo,
  ProviderInput, ProviderView, SessionSummary, Status, TestResult, ToolInfo, GroupConfig, GroupInput, GroupMember, GroupStatus, GroupStrategy, GroupView, MemberState, MemberStatus, ProxyView, UsageBy, UsageNode, UsageRange, UsageReport, UsageRequest, UsageTotals, ReleaseNotes, ChangeKind, UpdateInfo, UpdateStatus, WorkspaceInfo,
} from "./types";

let instance: Backend | null = null;

export function backend(): Backend {
  if (!instance) instance = isWails() ? createWailsBackend() : createDemoBackend();
  return instance;
}
