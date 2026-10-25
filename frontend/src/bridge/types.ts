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
  workspace: string;
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

/** 对应 settings.General。数字为 0、字符串为空表示"未设置，沿用默认值" */
export interface General {
  workspace?: string;
  max_steps?: number;
  max_tokens?: number;
  shell_timeout_seconds?: number;
  system_prompt?: string;
  /** 会话标题：first_message（截取第一句，默认）/ model（由模型生成） */
  title_mode?: "" | "first_message" | "model";
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
  loadSession(id: string): Promise<Status>;
  deleteSession(id: string): Promise<void>;
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
  /** 在浏览器中打开 ChatGPT 的用量页面（地址由后端固定） */
  openChatGPTUsage(): Promise<void>;
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
