// 桌面端桥接：调用 Wails 注入的 window.go 和 window.runtime。
//
// 没有使用 `wails generate module` 生成的 wailsjs 绑定，而是手写这一小层：
//   - 绑定方法不多（四十个左右，都是一行转发），手写的类型更精确（生成的代码会把 Go 结构体变成 class）；
//   - 前端因此可以在没有 Wails 的环境里构建和类型检查（云端 CI、浏览器演示）。
// 代价是 Go 端方法签名变化时要手动同步这里——方法很少，可以接受。

import type { AgentEvent, Decision } from "../protocol/events";
import { isKnownEvent } from "../protocol/events";
import { createReorderBuffer } from "./reorder";
import type {
  Backend, GroupInput, GroupStatus, GroupView, ProxyView, UsageReport, UsageRequest, UpdateInfo, ReleaseNotes, UpdateStatus, ChatGPTStatus, ChatGPTUsage, ContextBreakdown, DataInfo, General, GeneralView, KeychainStatus, MCPServerConfig, MCPServerStatus, PermissionMode, PermissionRule, ProfileInfo,
  ProviderInput, ProviderView, SessionSummary, Status, TestResult, ToolInfo, WorkspaceInfo,
} from "./types";

/** Go 端 internal/app.EventName */
const EVENT_NAME = "agent:event";

interface GoApp {
  ProtocolVersion(): Promise<number>;
  GetStatus(): Promise<Status>;
  GetProfiles(): Promise<ProfileInfo[] | null>;
  ListSessions(): Promise<SessionSummary[] | null>;
  NewSession(): Promise<Status>;
  LoadSession(id: string): Promise<Status>;
  DeleteSession(id: string): Promise<void>;
  RenameSession(id: string, title: string): Promise<void>;
  ReadWorkspaceImage(path: string): Promise<string>;
  OpenExternalURL(url: string): Promise<void>;
  SetModel(profile: string, model: string): Promise<Status>;
  Send(text: string): Promise<void>;
  Cancel(): Promise<void>;
  RespondPermission(requestId: string, decision: string): Promise<void>;
  ListProviders(): Promise<ProviderView[] | null>;
  SaveProvider(input: ProviderInput): Promise<void>;
  DeleteProvider(name: string): Promise<void>;
  TestProvider(input: ProviderInput): Promise<TestResult>;
  FetchModels(input: ProviderInput): Promise<string[] | null>;
  GetWorkspaceInfo(): Promise<WorkspaceInfo>;
  SetPermissionMode(mode: PermissionMode): Promise<Status>;
  GetStatusLineConfig(): Promise<unknown>;
  SaveStatusLineConfig(cfg: unknown): Promise<void>;
  GetGeneral(): Promise<GeneralView>;
  SaveGeneral(g: General): Promise<GeneralView>;
  ChooseDirectory(start: string): Promise<string>;
  SetSessionWorkspace(dir: string): Promise<Status>;
  GetDataInfo(): Promise<DataInfo>;
  OpenDataDir(): Promise<void>;
  ClearSessions(): Promise<number>;
  GetChatGPT(): Promise<ChatGPTStatus>;
  LoginChatGPT(newAccount: boolean): Promise<string>;
  CancelChatGPTLogin(): Promise<void>;
  LogoutChatGPT(): Promise<void>;
  GetChatGPTUsage(): Promise<ChatGPTUsage>;
  SetCodexQuota(on: boolean): Promise<void>;
  OpenChatGPTUsage(): Promise<void>;
  GetUsageReport(req: UsageRequest): Promise<UsageReport>;
  SaveUsageCSV(req: UsageRequest): Promise<string>;
  GetGroups(): Promise<GroupView[]>;
  SaveGroup(input: GroupInput): Promise<void>;
  DeleteGroup(name: string): Promise<void>;
  GetGroupStatuses(): Promise<GroupStatus[]>;
  ResetGroupMember(profile: string, model: string): Promise<void>;
  GetProxy(): Promise<ProxyView>;
  SetProxy(raw: string): Promise<void>;
  GetVersion(): Promise<string>;
  CheckUpdate(): Promise<UpdateInfo>;
  GetVersionHistory(): Promise<ReleaseNotes[] | null>;
  InstallUpdate(): Promise<void>;
  GetUpdateStatus(): Promise<UpdateStatus>;
  GetContextBreakdown(): Promise<ContextBreakdown>;
  ListTools(): Promise<ToolInfo[] | null>;
  SetDisabledTools(names: string[]): Promise<void>;
  GetPermissionRules(): Promise<PermissionRule[] | null>;
  SetPermissionRules(rules: PermissionRule[]): Promise<void>;
  GetStatusLineScript(): Promise<string>;
  SetStatusLineScript(cmd: string): Promise<void>;
  RunStatusLine(extra: Record<string, unknown>): Promise<string>;
  TestStatusLineScript(cmd: string): Promise<string>;
  ListMCPServers(): Promise<MCPServerConfig[] | null>;
  SaveMCPServers(list: MCPServerConfig[]): Promise<void>;
  GetMCPStatus(): Promise<MCPServerStatus[] | null>;
  ReconnectMCP(name: string): Promise<void>;
  GetUIPrefs(): Promise<unknown>;
  SaveUIPrefs(prefs: unknown): Promise<void>;
  GetKeychain(): Promise<KeychainStatus>;
  SetUseKeychain(on: boolean): Promise<KeychainStatus>;
}

interface WailsWindow {
  go?: { app?: { App?: GoApp } };
  runtime?: {
    EventsOn(name: string, cb: (...data: unknown[]) => void): () => void;
  };
}

/** Wails 运行时会在页面加载前注入 window.go / window.runtime。 */
export function isWails(): boolean {
  const w = window as unknown as WailsWindow;
  return !!w.go?.app?.App && !!w.runtime;
}

export function createWailsBackend(): Backend {
  const w = window as unknown as WailsWindow;
  const app = w.go!.app!.App!;
  const runtime = w.runtime!;
  return {
    kind: "wails",
    protocolVersion: () => app.ProtocolVersion(),
    getStatus: () => app.GetStatus(),
    getProfiles: async () => (await app.GetProfiles()) ?? [],
    listSessions: async () => (await app.ListSessions()) ?? [],
    newSession: () => app.NewSession(),
    loadSession: (id) => app.LoadSession(id),
    deleteSession: (id) => app.DeleteSession(id),
    renameSession: (id, title) => app.RenameSession(id, title),
    readWorkspaceImage: (path) => app.ReadWorkspaceImage(path),
    openExternalURL: (url) => app.OpenExternalURL(url),
    setModel: (p, m) => app.SetModel(p, m),
    send: (t) => app.Send(t),
    cancel: () => app.Cancel(),
    respondPermission: (id, d: Decision) => app.RespondPermission(id, d),
    listProviders: async () => (await app.ListProviders()) ?? [],
    saveProvider: (input) => app.SaveProvider(input),
    deleteProvider: (name) => app.DeleteProvider(name),
    testProvider: (input) => app.TestProvider(input),
    fetchModels: async (input) => (await app.FetchModels(input)) ?? [],
    getWorkspaceInfo: () => app.GetWorkspaceInfo(),
    setPermissionMode: (mode) => app.SetPermissionMode(mode),
    getStatusLineConfig: () => app.GetStatusLineConfig(),
    saveStatusLineConfig: (cfg) => app.SaveStatusLineConfig(cfg),
    getGeneral: () => app.GetGeneral(),
    saveGeneral: (g) => app.SaveGeneral(g),
    chooseDirectory: (start) => app.ChooseDirectory(start),
    setSessionWorkspace: (dir) => app.SetSessionWorkspace(dir),
    getDataInfo: () => app.GetDataInfo(),
    openDataDir: () => app.OpenDataDir(),
    clearSessions: () => app.ClearSessions(),
    getChatGPT: () => app.GetChatGPT(),
    loginChatGPT: (newAccount) => app.LoginChatGPT(newAccount),
    cancelChatGPTLogin: () => app.CancelChatGPTLogin(),
    logoutChatGPT: () => app.LogoutChatGPT(),
    getChatGPTUsage: () => app.GetChatGPTUsage(),
    setCodexQuota: (on) => app.SetCodexQuota(on),
    openChatGPTUsage: () => app.OpenChatGPTUsage(),
    getUsageReport: (req) => app.GetUsageReport(req),
    saveUsageCSV: (req) => app.SaveUsageCSV(req),
    getGroups: () => app.GetGroups(),
    saveGroup: (input) => app.SaveGroup(input),
    deleteGroup: (name) => app.DeleteGroup(name),
    getGroupStatuses: () => app.GetGroupStatuses(),
    resetGroupMember: (profile, model) => app.ResetGroupMember(profile, model),
    getProxy: () => app.GetProxy(),
    setProxy: (raw) => app.SetProxy(raw),
    getVersion: () => app.GetVersion(),
    checkUpdate: () => app.CheckUpdate(),
    getVersionHistory: async () => (await app.GetVersionHistory()) ?? [],
    installUpdate: () => app.InstallUpdate(),
    getUpdateStatus: () => app.GetUpdateStatus(),
    getContextBreakdown: () => app.GetContextBreakdown(),
    listTools: async () => (await app.ListTools()) ?? [],
    setDisabledTools: (names) => app.SetDisabledTools(names),
    getPermissionRules: async () => (await app.GetPermissionRules()) ?? [],
    setPermissionRules: (rules) => app.SetPermissionRules(rules),
    getStatusLineScript: () => app.GetStatusLineScript(),
    setStatusLineScript: (cmd) => app.SetStatusLineScript(cmd),
    runStatusLine: (extra) => app.RunStatusLine(extra),
    testStatusLineScript: (cmd) => app.TestStatusLineScript(cmd),
    listMCPServers: async () => (await app.ListMCPServers()) ?? [],
    saveMCPServers: (list) => app.SaveMCPServers(list),
    getMCPStatus: async () => (await app.GetMCPStatus()) ?? [],
    reconnectMCP: (name) => app.ReconnectMCP(name),
    getUIPrefs: () => app.GetUIPrefs(),
    saveUIPrefs: (prefs) => app.SaveUIPrefs(prefs),
    getKeychain: () => app.GetKeychain(),
    setUseKeychain: (on) => app.SetUseKeychain(on),
    onEvent(cb: (ev: AgentEvent) => void) {
      // 先按 seq 排序再过滤：未知类型的事件也占一个 seq，先过滤会让排序以为丢了事件而多等。
      const ordered = createReorderBuffer<{ seq: number }>((data) => {
        // 不认识的事件类型直接忽略（协议约定的向前兼容）。
        if (isKnownEvent(data)) cb(data);
      });
      return runtime.EventsOn(EVENT_NAME, (data) => ordered(data as { seq: number }));
    },
  };
}
