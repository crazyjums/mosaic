// Package codex 把 OpenAI 的 Codex CLI 接成 Mosaic 的 agent 引擎。
//
// 做法：每一轮启动一个 `codex app-server` 子进程（官方的、给编辑器和第三方客户端用的协议，stdio 上一行一条 JSON-RPC），
// 开线程（或按保存的线程 ID 恢复）、发用户的话，然后把它推过来的通知翻译成 Mosaic 的统一事件，
// 把它的审批请求（执行命令、改文件）转成 Mosaic 的权限确认。登录、模型、沙箱都是 Codex 自己的：
// Mosaic 不读也不保存 Codex 的凭证（和 internal/app/claudecode.go 对 Claude Code 的态度一致）。
//
// 为什么每轮一个进程而不是常驻：线程历史由 Codex 存在它自己的 CODEX_HOME 里，按线程 ID 恢复即可，
// 每轮多花约一秒启动；换来的是没有跨会话共享的进程状态，取消、崩溃、更新 Codex 都不会影响别的会话。
//
// 协议细节见 docs/design/harness.md；协议本身可以用 `codex app-server generate-json-schema` 生成。
package codex
