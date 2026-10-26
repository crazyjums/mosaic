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
  Backend, GroupInput, GroupStatus, GroupView, ProxyView, UsageReport, UsageRequest, UpdateInfo, SkillInfo, Attachment, ReleaseNotes, UpdateStatus, ChatGPTStatus, ChatGPTUsage, ContextBreakdown, DataInfo, General, GeneralView, KeychainStatus, MCPServerConfig, MCPServerStatus, PermissionMode, PermissionRule, ProfileInfo,
  ProviderInput, ProviderView, SessionSummary, Status, TestResult, ToolInfo, WorkspaceInfo,
  WorkspaceFile, PluginInfo, PluginPreview, PluginCatalogEntry, PluginStatus, PluginActionResult, SlashCommandInfo, GitOverview, GitCommit, GitCommitDetail, GitDiff,
} from "./types";

/** Go 端 internal/app.EventName */
const EVENT_NAME = "agent:event";

interface GoApp {
  ProtocolVersion(): Promise<number>;
  GetStatus(): Promise<Status>;
  GetProfiles(): Promise<ProfileInfo[] | null>;
  ListSessions(): Promise<SessionSummary[] | null>;
  NewSession(): Promise<Status>;
  LoadSession(id: string): Promise<{ status: Status; events: unknown[] | null; earlier: number }>;
  LoadEarlierHistory(sessionId: string): Promise<{ events: unknown[] | null; earlier: number }>;
  DeleteSession(id: string): Promise<void>;
  RenameSession(id: string, title: string): Promise<void>;
  ReadWorkspaceImage(path: string): Promise<string>;
  OpenExternalURL(url: string): Promise<void>;
  SetModel(profile: string, model: string): Promise<Status>;
  Send(text: string): Promise<void>;
  Cancel(): Promise<void>;
  CancelSession(sessionId: string): Promise<void>;
  SendToSession(sessionId: string, text: string, attachments: Attachment[]): Promise<void>;
  RespondPermission(requestId: string, decision: string): Promise<void>;
  ListProviders(): Promise<ProviderView[] | null>;
  SaveProvider(input: ProviderInput): Promise<void>;
  DeleteProvider(name: string): Promise<void>;
  TestProvider(input: ProviderInput): Promise<TestResult>;
  FetchModels(input: ProviderInput): Promise<string[] | null>;
  GetWorkspaceInfo(): Promise<WorkspaceInfo>;
  ReadWorkspaceText(path: string): Promise<WorkspaceFile>;
  OpenWorkspaceFile(path: string): Promise<void>;
  ListPlugins(): Promise<PluginInfo[] | null>;
  ListPluginCatalog(): Promise<PluginCatalogEntry[] | null>;
  RefreshPluginCatalog(): Promise<PluginCatalogEntry[] | null>;
  PreviewCatalogPlugin(id: string): Promise<PluginPreview>;
  PreviewPlugin(src: string): Promise<PluginPreview>;
  InstallPlugin(token: string, approved: string[]): Promise<PluginInfo>;
  CancelPluginPreview(token: string): Promise<void>;
  SetPluginEnabled(id: string, on: boolean): Promise<PluginInfo>;
  UninstallPlugin(id: string, purge: boolean): Promise<void>;
  SavePluginConfig(id: string, values: Record<string, string>): Promise<PluginInfo>;
  ClearPluginSecret(id: string, key: string): Promise<PluginInfo>;
  GetPluginStatus(id: string): Promise<PluginStatus>;
  RunPluginAction(id: string, action: string): Promise<PluginActionResult>;
  ListCommands(): Promise<SlashCommandInfo[] | null>;
  ExpandCommand(name: string, args: string): Promise<{ found: boolean; text: string }>;
  ChooseFile(title: string, patternName: string, pattern: string): Promise<string>;
  GitOverview(): Promise<GitOverview>;
  GitLog(skip: number, limit: number): Promise<GitCommit[] | null>;
  GitCommit(hash: string): Promise<GitCommitDetail>;
  GitCommitDiff(hash: string, path: string): Promise<GitDiff>;
  GitWorkingDiff(path: string, staged: boolean): Promise<GitDiff>;
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
  ListSkills(): Promise<SkillInfo[] | null>;
  SendMessage(text: string, attachments: Attachment[]): Promise<void>;
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

/** 后端返回的重放事件：空历史是 null；和实时事件一样丢弃不认识的类型。 */
const toEvents = (raw: unknown[] | null): AgentEvent[] => (raw ?? []).filter(isKnownEvent);

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
    loadSession: async (id) => {
      const r = await app.LoadSession(id);
      return { status: r.status, events: toEvents(r.events), earlier: r.earlier };
    },
    loadEarlierHistory: async (sessionId) => {
      const r = await app.LoadEarlierHistory(sessionId);
      return { events: toEvents(r.events), earlier: r.earlier };
    },
    deleteSession: (id) => app.DeleteSession(id),
    renameSession: (id, title) => app.RenameSession(id, title),
    readWorkspaceImage: (path) => app.ReadWorkspaceImage(path),
    readWorkspaceText: (path) => app.ReadWorkspaceText(path),
    openWorkspaceFile: (path) => app.OpenWorkspaceFile(path),
    openExternalURL: (url) => app.OpenExternalURL(url),
    setModel: (p, m) => app.SetModel(p, m),
    send: (sid, t, atts) => app.SendToSession(sid, t, atts ?? []),
    cancel: (sid) => (sid ? app.CancelSession(sid) : app.Cancel()),
    respondPermission: (id, d: Decision) => app.RespondPermission(id, d),
    listProviders: async () => (await app.ListProviders()) ?? [],
    saveProvider: (input) => app.SaveProvider(input),
    deleteProvider: (name) => app.DeleteProvider(name),
    testProvider: (input) => app.TestProvider(input),
    fetchModels: async (input) => (await app.FetchModels(input)) ?? [],
    getWorkspaceInfo: () => app.GetWorkspaceInfo(),
    listPlugins: async () => (await app.ListPlugins()) ?? [],
    listPluginCatalog: async () => (await app.ListPluginCatalog()) ?? [],
    refreshPluginCatalog: async () => (await app.RefreshPluginCatalog()) ?? [],
    previewCatalogPlugin: (id) => app.PreviewCatalogPlugin(id),
    previewPlugin: (src) => app.PreviewPlugin(src),
    installPlugin: (token, approved) => app.InstallPlugin(token, approved),
    cancelPluginPreview: (token) => app.CancelPluginPreview(token),
    setPluginEnabled: (id, on) => app.SetPluginEnabled(id, on),
    uninstallPlugin: (id, purge) => app.UninstallPlugin(id, purge),
    savePluginConfig: (id, values) => app.SavePluginConfig(id, values),
    clearPluginSecret: (id, key) => app.ClearPluginSecret(id, key),
    getPluginStatus: (id) => app.GetPluginStatus(id),
    runPluginAction: (id, action) => app.RunPluginAction(id, action),
    listCommands: async () => (await app.ListCommands()) ?? [],
    expandCommand: (name, args) => app.ExpandCommand(name, args),
    chooseFile: (title, patternName, pattern) => app.ChooseFile(title, patternName, pattern),
    gitOverview: async () => {
      const o = await app.GitOverview();
      return { status: { ...o.status, files: o.status.files ?? [] }, branches: o.branches ?? [] };
    },
    gitLog: async (skip, limit) => (await app.GitLog(skip, limit)) ?? [],
    gitCommit: async (hash) => {
      const d = await app.GitCommit(hash);
      return { ...d, files: d.files ?? [] };
    },
    gitCommitDiff: (hash, path) => app.GitCommitDiff(hash, path),
    gitWorkingDiff: (path, staged) => app.GitWorkingDiff(path, staged),
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
    listSkills: async () => (await app.ListSkills()) ?? [],
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
