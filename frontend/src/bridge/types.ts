// 前端与后端之间的"命令接口"。
//
// 前端组件和 store 只依赖这个接口，不直接接触 Wails：
//   - wails.ts：桌面端实现，调用 Go 绑定（window.go.app.App.*）并监听 agent:event；
//   - demo.ts：浏览器演示实现，回放 fake provider 录制的事件，不需要后端。
//
// 方法与 Go 端 internal/app/app.go 中 App 的公开方法一一对应。

import type { AgentEvent, Decision, Usage } from "../protocol/events";

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
  /** 订阅事件，返回取消订阅函数 */
  onEvent(cb: (ev: AgentEvent) => void): () => void;
}
