# 外部 agent 引擎（Harness）：Codex、Claude Code、pi

状态：**Codex 已实现**（§3）；**Claude Code 已实现**（§4，仅 Electron 版）；**pi 已实现**（§5，仅 Electron 版）。

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

**引擎没见过的对话会补给它**：会话的 `harness_state` **按引擎分开存**——每个引擎各记自己的恢复凭据，以及内核记的“这个引擎上次看到会话的前 N 条消息”（Codex 的线程 ID 不能当成 Claude Code 的会话 ID；Codex → Claude Code → 回到 Codex 时，各自恢复自己的线程，只补各自错过的那一段）。v1.7.20 存的旧格式（不分引擎）按 Codex 读取。新会话第一次交给引擎、或会话在别的引擎里聊过几轮再切回来时，这一轮的输入前会加一段背景（只含文字、工具调用名和截断的工具结果，不含别家的思考块；超过 2 万字只保留最近的）。会话被压缩过、消息数少于记录的 N 时无法判断缺了什么，不补。

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

## 4. 远程引擎的通道，与 Claude Code

Claude Code 和 pi 的官方 SDK 是 Node 包，Go 不能直接调用。做法：SDK 跑在 Electron 的 `utilityProcess` 里（每一轮一个进程），Go 这边只是一个很薄的 `Harness`（`internal/harness/remote`），把一轮对话变成发给壳的反向请求。所以它们只在 Electron 版里可用；Wails 版和命令行里对应的配置档会给出明确的提示。

```
Go（Run）──host.harness.run {run_id, spec}──▶ 壳（HarnessManager）──▶ utilityProcess（SDK）
Go ◀──harness.emit / harness.ask── 壳 ◀── utilityProcess
Go（用户点停止）──host.harness.cancel──▶ 壳 ──cancel──▶ worker
```

- **注册**：壳启动后端后发 `harness.register {engines}`（能提供的引擎，目前 `claudecode`、`pi`），Go 才把这些类型的会话交给它们（`Engine.RegisterHarness`）。Go 端写死白名单，壳报告什么都不会无条件注册。
- **安全**：`harness.*` 只有壳的主进程调得到（渲染进程的白名单是握手时的绑定方法，里面没有）；Go 校验 `run_id` 必须是进行中的轮、事件类型只能是引擎有权发的那几种（不能伪造 `turn_end`、`permission_*`），审批请求跟着这一轮的 ctx 一起取消。
- **顺序**：worker 的事件在壳里按顺序一个接一个送到 Go，结束前等它们送完，所以 `turn_end` 不会跑到事件前面；等用户回答的审批不挡住后面的事件。
- **取消**：用户点停止 → `host.harness.cancel` → worker 中止 SDK、等待中的确认按拒绝处理 → 交回已有的内容；8 秒内没交回就强制结束，按已取消返回。

### 4.1 Claude Code（`desktop/src/workers/claudecode.ts`）

- **用的是真正的官方 SDK**（`@anthropic-ai/claude-agent-sdk`，`query()` 加 `canUseTool`），不是自己重写。
- **不打包原生 `claude`**：SDK 的平台包里是 300MB 以上的原生可执行文件，不进安装包；用用户自己装的 `claude` 命令（PATH 里找，或配置档的「Claude Code 命令」）。SDK 本体和 worker 放在 `app.asar.unpacked`，因为 utilityProcess 要像普通进程一样 import 它们。
- **登录与计费是用户自己的**：用用户的 Claude Code 登录或环境里的 `ANTHROPIC_API_KEY`；Mosaic 不读不存。请按 Anthropic 的使用条款使用（第三方应用通常应使用 API Key 方式认证）。
- **事件映射**（依据对真实 SDK 0.3.319 + claude 2.1.319 的探测）：`stream_event` 的文字 / 思考增量 → `text_delta` / `thinking_delta`；`assistant` 里完整的 `tool_use` → `tool_call`；`user` 里的 `tool_result` → `tool_result`；`result` → 用量、会话 ID、成败。子 agent 里的过程（`parent_tool_use_id` 非空）不显示。
- **权限映射**：只读 → SDK 的 `plan`（可读不可改不可执行）；默认 → `default`；自动接受编辑（Mosaic 的取值是 `accept_edits`）→ SDK 的 `acceptEdits`；自动 → `bypassPermissions`（与 Mosaic 自动模式“放行一切”一致）。SDK 的 `canUseTool` 变成 Mosaic 的权限确认；「本会话允许」把 SDK 建议的权限更新交回去，同类操作不再询问。Claude Code 自己的设置（`CLAUDE.md`、MCP、技能、它自己的权限规则）照常生效。
- **恢复**：用 SDK 的会话 ID 作为恢复凭据（`options.resume`）。被打断（取消、出错）时，已经显示给用户但 SDK 还没给出完整块的文字 / 思考也会进历史，重新加载不会丢。
- **验证**：翻译器用真实探测的消息形状写了测试；worker 用假 SDK 覆盖审批、取消、错误；`HarnessManager` 用假 worker 覆盖顺序、审批、取消、崩溃；Go 端用假壳走完整的 stdio 流程。**真机端到端**（`desktop/scripts/harness-e2e.mjs`，会真的调用一次模型，不在 CI 里跑）：打包后的 Electron 应用 → Go → utilityProcess 里的 SDK → 真实 `claude`，写文件、权限确认走 Mosaic 弹窗、第二轮能记得上一轮做过什么（恢复有效）。
- **没验证**：Windows / macOS 上 SDK 启动 `claude`（Windows 的 `claude.cmd` 脚本可能需要经过 shell，优先找 `.exe`）；取消的真实时序（取消只有单元测试覆盖）。

## 5. pi（`desktop/src/workers/pi.ts`）

走 §4 的通道，worker 里跑 pi 的官方 SDK（`@earendil-works/pi-coding-agent`，旧包名 `@mariozechner/pi-coding-agent` 已弃用），`createAgentSession()` 在进程内运行，不需要用户另装命令。

- **模型和登录是用户自己的 pi 配置**（`~/.pi/agent` 下的 `settings.json`、`auth.json`、`models.json`）：Mosaic 不读不存。模型名填 `default` 用 pi 的默认模型，或写成 `提供商/模型ID`（`ModelRuntime.getModel`）；没有任何可用模型时提示去终端 `pi` 里 `/login`。
- **权限**：pi 自己没有逐次确认的机制（它的安全文档明说“不会在每次调用前询问”）。这里注册一个内联扩展，在 `tool_call` 钩子里把调用变成 Mosaic 的权限确认，拒绝则返回 `{block: true}`（模型会收到“用户拒绝了这次操作”的工具结果）。规则：默认模式 → 写类和执行类都问；自动接受编辑 → 只问执行类；自动 → 都不问；只读 → 只开 `read/grep/find/ls` 四个工具，不留写/执行工具。「本会话允许」按工具名记在这一轮的 worker 里。
- **事件映射**（依据对真实 SDK 1.5.0 的探测：假的 OpenAI 兼容服务器 + 真 pi）：`message_start`（助手）→ `message_start`；`message_update` 里的 `text_delta` / `thinking_delta` → 对应增量，`toolcall_end` → `tool_call`；`tool_execution_end` → `tool_result`（含被拦截时的错误结果）；`message_end`（助手）→ `message_end` 和用量；`system` / `user` 消息不转发。pi 不抛模型错误（鉴权失败、额度用完），错误在最后一条助手消息的 `errorMessage` 里，worker 据此报错。
- **恢复**：pi 的会话是 JSONL 文件，路径作为恢复凭据（`SessionManager.open`）；文件被清理了：worker 什么都不做并报告 `resume_lost`，内核（`driveHarness`）丢掉旧凭据，把完整历史当作“没见过”补给引擎再重跑一次（只重试一次），不会悄悄丢上下文。会话文件在 pi 自己的目录里，也可以用 pi 命令行接着聊。
- **打包**：pi 依赖较多，`node_modules` 整体放在 `app.asar.unpacked`（约 60MB）；pi-tui 自带的剪贴板原生模块（终端界面用，按架构各一份）排除掉——它会让 mac 通用包合并失败，worker 里也用不到。
- **验证**：翻译器和 worker 用真实探测的事件形状写了测试；真 SDK + 假模型服务器验证过：拒绝时文件没写、允许时写入、下一轮能记得上一轮（恢复有效）。
- **没验证**：用真实模型账号跑一轮（环境里没有 pi 的凭证）；Windows / macOS；取消的真实时序；图片附件和子 agent 不支持。
