// 前端与后端之间的"命令接口"。
//
// 前端组件和 store 只依赖这个接口，不直接接触 Wails：
//   - wails.ts：桌面端实现，调用 Go 绑定（window.go.app.App.*）并监听 agent:event；
//   - demo.ts：浏览器演示实现，回放 fake provider 录制的事件，不需要后端。
//
// 方法与 Go 端 internal/app/app.go 中 App 的公开方法一一对应。

import type { AgentEvent, Decision, Risk, Usage } from "../protocol/events";

/** 对应 permission.Mode：权限档位，从保守到放开 */
export type PermissionMode = "readonly" | "default" | "accept_edits" | "auto";

/** 对应 core.Status */
export interface Status {
  session_id: string;
  title: string;
  provider: string;
  model: string;
  usage: Usage;
  running: boolean;
  /** 当前会话实际使用的工作区 */
  workspace: string;
  /** 当前会话没有单独选过工作区，跟随默认工作区 */
  workspace_is_default?: boolean;
  /** 工作区由启动参数指定，不能为会话单独选择 */
  workspace_locked?: boolean;
  /** 工作区目录不存在（被移动或删除了） */
  workspace_missing?: boolean;
  pending_permissions: string[] | null;
  /** default / auto / readonly */
  permission_mode: PermissionMode;
}

/** 对应 provider.ProfileInfo（不含密钥） */
export interface ProfileInfo {
  name: string;
  type: string;
  models: string[] | null;
  default_model: string;
  has_api_key: boolean;
  available: boolean;
  error?: string;
  /** 每个模型的上下文窗口与单价（美元 / 百万 token），只包含配置过的模型 */
  model_meta: Record<string, ModelMeta> | null;
}

/** 对应 provider.ModelMeta */
export interface ModelMeta {
  context_window?: number;
  input_price?: number;
  output_price?: number;
  /** 缓存读 / 写的单价；没填时按输入价计算（估算偏高而不是偏低） */
  cache_read_price?: number;
  cache_write_price?: number;
}

/** 对应 workspace.Info */
export interface WorkspaceInfo {
  path: string;
  name: string;
  is_repo: boolean;
  branch?: string;
  dirty: boolean;
  changed_files: number;
}

/** 一批重放事件（时间顺序），加上这批之前还有多少轮没加载 */
export interface HistoryPage {
  events: AgentEvent[];
  earlier: number;
}

export interface LoadedSession extends HistoryPage {
  status: Status;
}

/** 一个技能（对应 skill.Info）：输入框里键入 / 时的补全项 */
export interface SkillInfo {
  name: string;
  description: string;
  /** project：工作区里的；user：个人目录里的 */
  source: "project" | "user";
  /** 所在的根目录，例如 ".myllmapp/skills"、".claude/skills" */
  origin: string;
  dir: string;
}

/** 对应 settings.General。数字为 0、字符串为空表示"未设置，沿用默认值" */
export interface General {
  workspace?: string;
  max_steps?: number;
  max_tokens?: number;
  shell_timeout_seconds?: number;
  system_prompt?: string;
  /** 会话标题：first_message（截取第一句，默认）/ model（由模型生成） */
  title_mode?: "" | "first_message" | "model";
  /** 打开历史会话时先加载的最近轮数，也是"加载更早的"每次加载的轮数 */
  history_turns?: number;
}

/** 对应 settings.GeneralView */
export interface GeneralView {
  saved: General;
  effective: General;
  defaults: General;
  default_system_prompt: string;
  workspace_locked: boolean;
  workspace_warning?: string;
}

/** 对应 settings.DataInfo */
export interface DataInfo {
  data_dir: string;
  settings_path: string;
  db_path: string;
  db_bytes: number;
  sessions: number;
}

/** 对应 core.SessionSummary */
export interface SessionSummary {
  id: string;
  title: string;
  provider: string;
  model: string;
  /** 会话的工作区；为空表示跟随默认工作区 */
  workspace?: string;
  message_count: number;
  created_at: string;
  updated_at: string;
}

/** 对应 settings.ProviderView：设置页里的一个厂商配置档（不含密钥明文） */
export interface ProviderView {
  name: string;
  type: string;
  base_url: string;
  models: string[];
  default_model: string;
  options: Record<string, unknown> | null;
  has_api_key: boolean;
  /** 掩码后的密钥，例如 "sk-…a1b2" */
  api_key_hint: string;
  /** config：来自 config.toml；settings：界面添加；override：界面覆盖了 config.toml；account：ChatGPT 账号登录 */
  source: "config" | "settings" | "override" | "account";
  deletable: boolean;
  available: boolean;
  error?: string;
}

/** 对应 settings.ProviderInput：设置页提交的表单 */
export interface ProviderInput {
  /** 编辑前的名称；新建时为空 */
  original_name: string;
  name: string;
  type: string;
  base_url: string;
  /** 留空表示沿用已保存的密钥 */
  api_key: string;
  models: string[];
  default_model: string;
  options: Record<string, unknown>;
}

/** 对应 settings.TestResult */
export interface TestResult {
  ok: boolean;
  model: string;
  latency_ms: number;
  reply?: string;
  error?: string;
}

/** 对应 core.ToolInfo */
export interface ToolInfo {
  name: string;
  description: string;
  risk: Risk;
  disabled: boolean;
}

/** 对应 permission.Rule：按工具名和命令 / 路径匹配，优先于权限档位 */
export interface PermissionRule {
  /** 工具名，支持 * 通配（例如 mcp__github__*） */
  tool: string;
  /** 匹配 command / path / url 字段，* 匹配任意字符；空 = 任意 */
  pattern: string;
  action: "allow" | "ask" | "deny";
}

/** 对应 mcp.ServerConfig */
export interface MCPServerConfig {
  name: string;
  transport: "stdio" | "http";
  command?: string;
  args?: string[] | null;
  env?: Record<string, string> | null;
  cwd?: string;
  url?: string;
  headers?: Record<string, string> | null;
  risk?: Risk;
  disabled?: boolean;
}

/** 对应 mcp.ServerStatus */
export interface MCPServerStatus {
  name: string;
  state: "connecting" | "connected" | "failed" | "disabled";
  error?: string;
  tools: string[];
  server_name?: string;
  server_version?: string;
}

/** 对应 settings.KeychainStatus */
export interface KeychainStatus {
  enabled: boolean;
  available: boolean;
  error?: string;
  warning?: string;
}

/** 对应 settings.ChatGPTStatus（内嵌 siwc.Status）。不含任何令牌 */
export interface ChatGPTStatus {
  state: "logged_out" | "pending" | "logged_in";
  email?: string;
  plan?: string;
  /** 等待登录时的授权地址，浏览器没有自动打开时可以复制 */
  login_url?: string;
  error?: string;
  /** 本机已有签发的 client_id，下次登录不用重新注册 */
  registered: boolean;
  models_error?: string;
}

/** 对应 siwc.UsageWindow */
export interface UsageWindow {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
}

/** 对应 settings.ChatGPTUsage：本应用用掉的 ChatGPT 套餐额度（OpenAI 没有提供查询剩余额度的接口） */
export interface ChatGPTUsage {
  logged_in: boolean;
  last_5h: UsageWindow;
  last_7d: UsageWindow;
  /** 最近一次遇到"额度已用完"的时间；之后成功调用过则为空 */
  limit_hit_at?: string;
  limit_message?: string;
  usage_url: string;
  /** 是否开启了"读取 Codex 登录查询剩余额度" */
  quota_enabled: boolean;
  quota?: Quota;
  quota_error?: string;
}

/** 对应 codexquota.Window */
export interface QuotaWindow {
  /** 窗口长度：5 小时 = 18000，每周 = 604800 */
  seconds: number;
  used_percent: number;
  reset_at?: string;
}

/** 对应 codexquota.Quota：ChatGPT 套餐的剩余额度（从 Codex 登录查询） */
export interface Quota {
  plan?: string;
  windows: QuotaWindow[] | null;
  credits?: number;
  fetched_at: string;
}

/** 对应 core.ContextPart */
export interface ContextPart {
  kind: "system" | "tools" | "user" | "assistant" | "thinking" | "tool_call" | "tool_result" | "image";
  count: number;
  tokens: number;
}

/** 对应 core.ContextItem */
export interface ContextItem {
  kind: ContextPart["kind"];
  /** user：第几条用户消息；assistant / thinking：第几次回答 */
  index?: number;
  /** tool_call / tool_result 的工具名 */
  tool?: string;
  detail: string;
  tokens: number;
}

/** 对应 core.ContextBreakdown：按字符估算的上下文组成 */
export interface ContextBreakdown {
  parts: ContextPart[] | null;
  largest: ContextItem[] | null;
  total: number;
  messages: number;
  tool_count: number;
}

export interface Backend {
  /** 用于在界面上标明当前是"桌面端"还是"浏览器演示" */
  readonly kind: "wails" | "demo";
  protocolVersion(): Promise<number>;
  getStatus(): Promise<Status>;
  getProfiles(): Promise<ProfileInfo[]>;
  listSessions(): Promise<SessionSummary[]>;
  newSession(): Promise<Status>;
  /** 加载历史会话：只返回最近若干轮的重放事件，更早的用 loadEarlierHistory 按需取 */
  loadSession(id: string): Promise<LoadedSession>;
  loadEarlierHistory(sessionId: string): Promise<HistoryPage>;
  deleteSession(id: string): Promise<void>;
  /** 修改会话标题（当前会话或历史会话）；成功后后端会发 session_updated */
  renameSession(id: string, title: string): Promise<void>;
  /** 读取当前会话工作区内的图片，返回 data URL（聊天里显示模型生成的 SVG / PNG） */
  readWorkspaceImage(path: string): Promise<string>;
  /** 在系统浏览器中打开 http / https 链接 */
  openExternalURL(url: string): Promise<void>;
  setModel(profile: string, model: string): Promise<Status>;
  /** 立即返回；对话过程通过 onEvent 推送 */
  send(text: string): Promise<void>;
  cancel(): Promise<void>;
  respondPermission(requestId: string, decision: Decision): Promise<void>;
  // ---- 设置：厂商配置 ----
  listProviders(): Promise<ProviderView[]>;
  saveProvider(input: ProviderInput): Promise<void>;
  deleteProvider(name: string): Promise<void>;
  testProvider(input: ProviderInput): Promise<TestResult>;
  fetchModels(input: ProviderInput): Promise<string[]>;
  // ---- 状态栏 ----
  getWorkspaceInfo(): Promise<WorkspaceInfo>;
  setPermissionMode(mode: PermissionMode): Promise<Status>;
  /** 从未保存过时返回 null */
  getStatusLineConfig(): Promise<unknown>;
  saveStatusLineConfig(cfg: unknown): Promise<void>;
  // ---- 设置：常规 / 系统提示词 / 数据 ----
  getGeneral(): Promise<GeneralView>;
  saveGeneral(g: General): Promise<GeneralView>;
  /** 为当前会话选择工作区；传空字符串表示跟随默认工作区 */
  setSessionWorkspace(dir: string): Promise<Status>;
  /** 弹出选择文件夹对话框；取消时返回空字符串 */
  chooseDirectory(start: string): Promise<string>;
  getDataInfo(): Promise<DataInfo>;
  openDataDir(): Promise<void>;
  /** 删除所有历史会话，返回删除的数量 */
  clearSessions(): Promise<number>;
  // ---- 设置：ChatGPT 账号登录 ----
  getChatGPT(): Promise<ChatGPTStatus>;
  /** 在系统浏览器中打开授权页面，返回授权地址 */
  loginChatGPT(newAccount: boolean): Promise<string>;
  cancelChatGPTLogin(): Promise<void>;
  logoutChatGPT(): Promise<void>;
  getChatGPTUsage(): Promise<ChatGPTUsage>;
  /** 开启或关闭"读取本机 Codex 登录查询剩余额度"（未公开接口） */
  setCodexQuota(on: boolean): Promise<void>;
  /** 在浏览器中打开 ChatGPT 的用量页面（地址由后端固定） */
  openChatGPTUsage(): Promise<void>;
  /** 当前版本号；开发构建是 "dev" */
  // ---- 用量统计、模型组（故障转移）、网络代理 ----
  getUsageReport(req: UsageRequest): Promise<UsageReport>;
  /** 弹出"另存为"对话框导出 CSV；返回保存的路径，取消时为空串 */
  saveUsageCSV(req: UsageRequest): Promise<string>;
  getGroups(): Promise<GroupView[]>;
  saveGroup(input: GroupInput): Promise<void>;
  deleteGroup(name: string): Promise<void>;
  getGroupStatuses(): Promise<GroupStatus[]>;
  /** 手动恢复一个成员：清掉冷却和停用状态 */
  resetGroupMember(profile: string, model: string): Promise<void>;
  getProxy(): Promise<ProxyView>;
  setProxy(raw: string): Promise<void>;
  getVersion(): Promise<string>;
  /** 检查更新：网络或签名错误以异常返回；disabled 非空表示更新功能不可用（开发版 / 没配置公钥） */
  checkUpdate(): Promise<UpdateInfo>;
  /** 当前会话可用的技能（随工作区变化，每次都重新扫描） */
  listSkills(): Promise<SkillInfo[]>;
  /** 随程序打包的全部历史版本更新日志，最新的在前；离线可用 */
  getVersionHistory(): Promise<ReleaseNotes[]>;
  /** 下载并安装最近一次检查到的版本，然后重启；立即返回，进度用 getUpdateStatus 轮询 */
  installUpdate(): Promise<void>;
  getUpdateStatus(): Promise<UpdateStatus>;
  // ---- 上下文明细 ----
  getContextBreakdown(): Promise<ContextBreakdown>;
  // ---- 设置：权限与工具 ----
  listTools(): Promise<ToolInfo[]>;
  setDisabledTools(names: string[]): Promise<void>;
  getPermissionRules(): Promise<PermissionRule[]>;
  setPermissionRules(rules: PermissionRule[]): Promise<void>;
  // ---- 设置：状态栏脚本 ----
  getStatusLineScript(): Promise<string>;
  setStatusLineScript(cmd: string): Promise<void>;
  /** 运行已保存的命令；extra 会和会话信息一起以 JSON 写到命令的标准输入 */
  runStatusLine(extra: Record<string, unknown>): Promise<string>;
  testStatusLineScript(cmd: string): Promise<string>;
  // ---- 设置：MCP ----
  listMCPServers(): Promise<MCPServerConfig[]>;
  saveMCPServers(list: MCPServerConfig[]): Promise<void>;
  getMCPStatus(): Promise<MCPServerStatus[]>;
  reconnectMCP(name: string): Promise<void>;
  // ---- 设置：外观与钥匙串 ----
  /** 从未保存过时返回 null */
  getUIPrefs(): Promise<unknown>;
  saveUIPrefs(prefs: unknown): Promise<void>;
  getKeychain(): Promise<KeychainStatus>;
  setUseKeychain(on: boolean): Promise<KeychainStatus>;
  /** 订阅事件，返回取消订阅函数 */
  onEvent(cb: (ev: AgentEvent) => void): () => void;
}

export type ChangeKind = "breaking" | "security" | "added" | "changed" | "fixed" | "deprecated" | "removed";

/** 一个版本的更新日志（Keep a Changelog 的分类） */
export interface ReleaseNotes {
  version: string;
  date?: string;
  summary?: string;
  sections: { kind: ChangeKind; items: string[] }[];
}

export interface UpdateInfo {
  current: string;
  latest: string;
  available: boolean;
  notes?: string;
  pub_date?: string;
  /** 当前版本之后到最新版本之间的全部更新日志，最新的在前 */
  releases?: ReleaseNotes[];
  /** 更新建议：critical 含安全修复 / recommended 含修复 / optional 只有新功能；旧格式清单没有 */
  level?: "critical" | "recommended" | "optional";
  /** 含不兼容变更，更新前需要先阅读 */
  breaking?: boolean;
  /** 这个平台不能自动更新（macOS），需要去 page 手动下载 */
  manual: boolean;
  page?: string;
  size?: number;
  disabled?: string;
}

export interface UpdateStatus {
  state: "idle" | "downloading" | "installing" | "restarting" | "error";
  done: number;
  total: number;
  error?: string;
}

export type UsageBy = "day" | "session" | "model" | "profile";
export type UsageRange = "today" | "7d" | "30d" | "all" | "custom";

export interface UsageRequest {
  range: UsageRange;
  from?: string;
  to?: string;
  by: UsageBy;
  profile?: string;
  model?: string;
  group?: string;
}

/** 对应 usage.Totals。输入 token 的口径是"含缓存读写"。 */
export interface UsageTotals {
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  requests: number;
  failed_tries: number;
  handed_on: number;
  /** 美元；换算人民币由界面按用户填的汇率做 */
  cost_usd: number;
  /** 没有价格、因而没有计入花费的成功调用数 */
  unpriced_calls: number;
  /** 按套餐计费、不计金额的成功调用数 */
  plan_calls: number;
  cache_hit: number;
}

export interface UsageNode extends UsageTotals {
  key: string;
  label: string;
  plan?: boolean;
  priced: boolean;
  children?: UsageNode[];
}

export interface UsageReport {
  by: UsageBy;
  totals: UsageTotals;
  nodes: UsageNode[];
  unpriced_models: string[];
  from: string;
  to: string;
  /** 范围内出现过的配置档，供筛选下拉框使用 */
  profiles: string[];
}

export interface GroupMember {
  profile: string;
  model: string;
}

export type GroupStrategy = "order" | "rotate" | "least_used";

export interface GroupConfig {
  name: string;
  members: GroupMember[];
  strategy: GroupStrategy;
  sticky: boolean;
  first_byte_timeout_s: number;
}

export interface GroupView extends GroupConfig {
  available: boolean;
  error?: string;
}

export interface GroupInput extends GroupConfig {
  original_name: string;
}

export type MemberState = "healthy" | "cooling" | "probing" | "disabled";

export interface MemberStatus {
  profile: string;
  model: string;
  state: MemberState;
  /** 冷却到期时间（Unix 毫秒） */
  until?: number;
  kind?: string;
  message?: string;
  fails?: number;
  /** 成员本身不可用的原因（配置档被删了、缺密钥…），与运行时的健康状态无关 */
  unavailable?: string;
}

export interface GroupStatus {
  name: string;
  strategy: string;
  members: MemberStatus[] | null;
}

export interface ProxyView {
  set: boolean;
  /** 密码已脱敏的地址 */
  display: string;
}
