# 外部 agent 引擎（Harness）：Codex、Claude Code、pi

状态：**Codex 已实现**（本文 §3）；Claude Code 和 pi 还没做（§4，接下来做）。

## 1. 为什么

Mosaic 内置的 agent 循环（`internal/core` 的 `Agent`）自己调用模型、自己执行工具。Codex、Claude Code、pi 这类产品把“循环 + 工具 + 沙箱 + 它们自己的提示词和上下文管理”做在了自己里面。想让用户在 Mosaic 里使用它们，不能再套一层自己的循环，只能把它们当黑盒引擎来驱动：

- 用户的话交给引擎；
- 引擎产生的过程（文字、思考、命令、改文件）翻译成 Mosaic 的统一事件，界面、会话历史、导出都不用改；
- 引擎的审批请求（要执行命令、要改文件）转成 Mosaic 的权限确认弹窗；
- 登录、模型、沙箱、配额都是引擎自己的：**Mosaic 不读也不保存它们的凭证**（和 `internal/app/claudecode.go` 对 Claude Code 的做法一致）。

## 2. 接口（`internal/harness`）

```go
type Harness interface {
    Name() string                                   // 和厂商配置档的 type 一致，例如 "codex"
    Run(ctx context.Context, req Request) Result    // 一轮；返回时这一轮已经结束
}
```

- `Request`：会话 ID、用户的话（文本附件已并入）、工作区、模型、Mosaic 的权限模式、配置档里的选项、**恢复凭据**（上一轮返回的不透明字符串，例如 Codex 的线程 ID）、`Emit(messageID, payload)`（发统一事件）、`Ask(ctx, permission.Request)`（请求用户确认，阻塞到回答）。
- `Result`：结束原因、错误、新的恢复凭据、本轮 token 用量、要追加进会话历史的消息。

**怎么选中引擎**：厂商配置档的 `type` 在 `EngineConfig.Harnesses` 里有对应项，这个配置档的会话整轮走引擎（`internal/core/harness.go`）；否则走内置循环。所以用户在界面里就是“添加一个 Codex 厂商”，再像选模型一样选它。

**内核负责的前后工作**：发 `turn_start` / `user_message`；把引擎的事件原样发布；`Ask` 接到 `permission.Broker`（发 `permission_request` / `permission_resolved`，用户的回答经原来的 `RespondPermission` 送达）；把引擎返回的消息存进历史、累加用量、保存恢复凭据（`sessions.harness_state` 列，迁移 4）；最后发 `turn_end`。

**引擎没见过的对话会补给它**：会话的 `harness_state` 里除了引擎的恢复凭据，内核还记着“引擎上次看到会话的前 N 条消息”。新会话第一次交给引擎、或会话在别的引擎里聊过几轮再切回来时，这一轮的输入前会加一段背景（只含文字、工具调用名和截断的工具结果，不含别家的思考块；超过 2 万字只保留最近的）。会话被压缩过、消息数少于记录的 N 时无法判断缺了什么，不补。

**历史的结构**与内置循环一致（助手消息 + 工具结果的 user 消息，每个 tool_use 都有 tool_result），所以会话重放、导出、以及切换到内置引擎继续聊都能用。

**限制**：目前不支持图片附件（引擎收不到，直接报错，不会悄悄丢掉）；不做上下文压缩、标题生成（引擎自己管上下文；标题用截取的第一句）。

## 3. Codex（`internal/harness/codex`）

### 3.1 协议

用官方的 `codex app-server`（给编辑器和第三方客户端用的协议，stdio 上一行一条 JSON-RPC）。协议定义可以用 `codex app-server generate-json-schema` 生成，本实现按 **0.167** 的 schema 写，并对真实进程做过握手探测。

每一轮：启动 `codex app-server` → `initialize` / `initialized` → `account/read`（没登录立刻提示 `codex login`，不用等它重试近一分钟）→ `thread/resume`（有恢复凭据）或 `thread/start` → `turn/start` → 一直处理通知到 `turn/completed` → 关 stdin 退出进程。

为什么每轮一个进程而不是常驻：线程历史由 Codex 存在它自己的 `CODEX_HOME` 里，按线程 ID 恢复即可，多花约一秒启动；换来的是没有跨会话共享的进程状态，取消、崩溃、升级 Codex 都不影响别的会话。

自己实现 JSON-RPC 客户端（`client.go`）而不复用 `internal/rpc.Peer`：Peer 为每条入站消息开 goroutine，处理顺序不定；这里的通知是流式输出，必须按到达顺序处理。通知在读循环里同步处理，只有要等用户回答的请求（审批）放到 goroutine 里。

### 3.2 事件映射

| Codex（item） | Mosaic |
| --- | --- |
| `agentMessage` + `item/agentMessage/delta` | 一条助手消息：`message_start`、`text_delta`…、`message_end` |
| `reasoning` + 两种 `…TextDelta` | `thinking_delta`（和紧跟着的回答在同一条消息里） |
| `commandExecution` | `tool_call`（`shell`，risk `exec`）… `tool_result`（命令输出、退出码；被拒绝则 is_error） |
| `fileChange` | `tool_call`（`apply_patch`，risk `write`）… `tool_result`（改了哪些文件） |
| `mcpToolCall` | `tool_call`（`mcp:<server>/<tool>`）… `tool_result` |
| `webSearch` | `tool_call`（`web_search`，risk `read`）… `tool_result` |
| `thread/tokenUsage/updated` | 本轮用量（输入 = 总输入 − 缓存命中） |
| `error`（`willRetry`）/ `warning` | `error` 事件（重试和警告非致命；最终失败是致命的） |
| `turn/completed`：completed / interrupted / failed | `turn_end`：completed / cancelled / error |

### 3.3 权限

| Mosaic 权限模式 | Codex 审批策略 | Codex 沙箱 |
| --- | --- | --- |
| 只读 | `on-request` | `read-only` |
| 默认 | `on-request` | `workspace-write` |
| 自动接受编辑 | `on-request` | `workspace-write`（Codex 没有“改文件免问、执行要问”的拆分，命令和越界写仍会问） |
| 自动 | `never` | `danger-full-access`（与 Mosaic 自动模式“放行一切”一致） |

Codex 的 `item/commandExecution/requestApproval` / `item/fileChange/requestApproval` 变成 Mosaic 的权限确认：允许 → `accept`，本会话允许 → `acceptForSession`，拒绝 → `decline`，停止 → `cancel`。其他需要界面的请求（向用户提问、MCP 征询、动态工具、令牌刷新）暂不支持：明确回错误并发一条提示事件，让 Codex 自己继续，而不是卡死。

### 3.4 取消

用户点停止：发 `turn/interrupt`，宽限 5 秒等 Codex 整理好并发 `turn/completed`；已经输出的内容照常进历史。进程意外退出：把 stderr 末尾几行拼进错误信息。

### 3.5 配置

设置 → 模型厂商 → 添加 → 协议选「Codex（本机 CLI）」：没有 Base URL 和 API Key；可选「Codex 命令」（默认在 PATH 里找 `codex`）；模型填 `default` 表示用 Codex 自己的默认模型，也可以填具体型号。先安装（`npm install -g @openai/codex`）并在终端执行 `codex login`。

### 3.6 验证

- 假的 `codex app-server`（测试二进制重新执行自己）覆盖：事件翻译与顺序、历史消息配对、审批三种回答、恢复与恢复失败后新开、取消、失败、崩溃、没登录、找不到命令。
- 内核侧用假引擎覆盖：生命周期与事件、权限经 Broker、错误与取消、图片被拒、文本附件并入。
- 真实 Codex 0.167：握手、`account/read`、没登录的提示（`MOSAIC_CODEX_BIN=… go test ./internal/harness/codex -run Real`）。
- **没验证**：真实登录后的完整对话（这个环境没有 Codex 凭证）。事件映射依据 schema 和假服务器，真实输出的细节（例如 `fileChange` 的 `kind` 形状）可能还需要在有账号的机器上校对。

## 4. Claude Code 和 pi（接下来）

两者的官方 SDK 是 Node 包，不能直接用 Go 驱动。方案（见 [electron-migration.md](electron-migration.md) 阶段 6）：SDK 跑在 Electron 的 `utilityProcess` 里，通过 `harness.*` 反向请求接回 Go——Go 侧是一个 `Harness` 实现，把 `Run` 转成发给主进程的请求，事件和权限回调沿同一条通道回来。因此它们只在 Electron 版里可用。
