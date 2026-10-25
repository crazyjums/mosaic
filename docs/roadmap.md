# 路线图与接口预留情况

## 第一阶段（当前）✅

- 单会话流式对话，切换厂商与模型（OpenAI 兼容 + Anthropic 原生 + fake）
- agent 循环：工具调用 → 执行 → 回传 → 继续，最大步数限制，取消
- 内置工具：read_file / list_dir / find_files / search_content / edit_file / write_file / mkdir / move_file / delete_file / shell / git_status / git_diff / git_log / todo_write / todo_read
- 权限：write/exec 前 `permission_request`，前端弹窗确认；`readonly` / `default` / `accept_edits` / `auto` 四档模式，可在状态栏或 CLI `/mode` 随时切换；本会话总是允许
- 前端：消息流、可折叠思考卡片、工具调用卡片、权限弹窗、状态栏（厂商/模型/token）、会话列表
- 会话保存与恢复（SQLite，纯 Go）
- CLI 调试入口；浏览器演示模式
- 设置页：界面上配置厂商（Base URL + API Key + 模型列表），获取模型列表、测试连接，修改立即生效，记住上次选择
- 用 ChatGPT 账号登录（Sign in with ChatGPT），用套餐额度调用 GPT 模型
- 发送队列（同一会话内排队）、图片与文本附件、上下文压缩、Git 窗口（只读）、思考摘要
- 设置页（续）：权限规则、工具开关、MCP 服务器、自定义状态栏脚本、API Key 存系统钥匙串、外观（主题/字号/宽度/思考卡片/发送键）、中英文界面、由模型生成会话标题

## 预留能力一览

| 能力 | 事件 | 后端接口 | 前端 | 还差什么 |
| --- | --- | --- | --- | --- |
| 子 agent 调度与可视化树 | ✅ `subagent_start/end`，信封 `agent_id`/`parent_id` | ✅ `SubagentDefinition`、`SubagentRegistry`；⬜ `SubagentRunner` | ✅ reducer 建树；`ActivityPanel` 显示 | Runner 实现 + `task` 工具 |
| 后台任务 | ✅ `task_start/log/end` | ⬜ `TaskManager` 接口 | ✅ reducer + 面板 | TaskManager 实现 + 3 个工具 |
| 上下文压缩 | ✅ `context_compacted` | ✅ `SummaryCompactor`（估算触发、摘要、保留最近几轮）；⬜ 手动“立即压缩”、按厂商返回的 input_tokens 校准、服务端压缩 | ✅ 提示 + 可展开摘要 | 见下文“上下文压缩” |
| MCP 接入 | — | ✅ stdio / Streamable HTTP 客户端、`mcp.Manager` | ✅ 设置 → MCP | resources / prompts、服务器推送（工具列表变化通知） |
| 插件：状态栏脚本 | — | ✅ `plugin.CommandStatusLine` | ✅ 状态栏"脚本输出"项 | — |
| 插件：技能 / 斜杠命令 / 钩子 | — | ⬜ `internal/plugin` 接口 | ⬜ | 加载器 + 各接入点 |

✅ 已有　⬜ 只有接口 / 未做

## 并发与排队

**目前支持“当前会话排队”，不支持多个会话同时运行。** `Engine` 同一时间只跑一轮，运行中再调用 `Send` 返回 `ErrBusy`——这是有意保留的保护，不是缺陷：
会话状态、事件订阅、权限确认、取消和工作区都是“当前会话”一份，直接放开会让事件串到别的会话、权限弹窗互相覆盖。

排队放在前端（[`store/queue.ts`](../frontend/src/store/queue.ts) 状态机 + `chatStore`）：消息按会话绑定、先进先出；只在后端真的发出 `turn_end` 后才发下一条；
发送中的条目已同步标记，防止重复发送；失败的条目留在队首，用户重试或删除前不会越过它；用户点“停止”或出错时队列暂停，不自动继续。排队只重排顺序，不会打断正在运行的一轮。

真正的多会话并发需要的改造：引擎按会话持有运行状态（`running`/`cancel`）、事件总线按会话分流订阅、权限请求带会话标识并分别排队、工作区与工具状态按会话隔离。做之前不会去掉 `ErrBusy`。

## 第二阶段计划

### 子 agent 调度与可视化树

接口：[`internal/core/subagent.go`](../internal/core/subagent.go)。

1. 实现 `SubagentRunner`：
   ```go
   func (r *runner) RunSubagent(ctx context.Context, req SubagentRequest) (SubagentResult, error) {
       id := event.NewID("agent")
       // 发 subagent_start{agent_id: id, parent_id: req.ParentAgentID, ...}
       agent := NewAgent(AgentConfig{
           ID: id, ParentID: req.ParentAgentID,
           Tools: r.restrictedTools(req.Definition.Tools),
           System: req.Definition.System, ... // 其余依赖与主 agent 相同
       })
       sess := NewSession(event.NewID("sess"), ...) // 子 agent 有独立的历史
       res := agent.Run(ctx, sess, req.Prompt)
       // 发 subagent_end，返回最后一条 assistant 消息的文本
   }
   ```
2. 提供一个 `task` 工具（参数 `{agent, prompt}`），内部调用 Runner；把可用的子 agent 定义写进工具描述。
3. 事件信封已经由 `emitter` 自动带上 `agent_id` / `parent_id`，**不需要修改 Agent**。
4. 前端：`AssistantMessage` 已显示非 main 的 agent 标签；`ActivityPanel` 已能画树。可以进一步把子 agent 的消息折叠到对应工具卡片下面。

需要决定：子 agent 的会话是否持久化；并行运行多个子 agent 时权限弹窗如何排队。

### 后台任务

接口：[`internal/core/task.go`](../internal/core/task.go)。

1. 实现 `TaskManager`：`os/exec` 启动进程，环形缓冲区保存最近日志，每行发 `task_log`（注意节流）；
2. 工具：`start_task`（exec 风险）、`task_status`、`stop_task`；
3. 会话结束/应用退出时清理所有任务；
4. 前端已有面板，可以增加"停止"按钮（新增绑定方法 `StopTask(id)`）。

### 上下文压缩 ✅（基础版）

实现：[`internal/core/compact.go`](../internal/core/compact.go) 的 `SummaryCompactor`，已在 `Agent.prepareContext` 中每步调用；`Engine` 每轮用本轮的厂商和模型创建它。

行为（都有测试，见 `compact_test.go`）：
1. **触发**：按字符估算（系统提示词 + 工具定义 + 历史）超过模型上下文窗口的 80%。窗口取自厂商配置里的 `models_meta.context_window`；**没配置就不压缩**（不猜窗口大小）；
2. **保留**：优先原样保留最近 2 轮；这些超过窗口的 40% 时（一轮里读了很多大文件）退到轮内更靠后的安全位置；
3. **切点**：只切在“用户发言”或“紧跟 user 消息的 assistant 消息”之前，所以 `tool_use` 和它的 `tool_result` 不会被拆开，保留部分也不会以孤立的 `tool_result` 开头；
4. **摘要**：把较早的消息转写成文本，用同一个厂商和模型生成摘要（已有的旧摘要会被合并而不是堆叠），替换成一条以固定标记开头的 user 消息（`provider.SummaryHeader`，不算“用户发言”）；
5. **持久化与回放**：替换后的历史随会话保存（`replaceMessages` 使 epoch 加一，分页游标失效）；恢复会话时摘要回放成 `context_compacted`（`replay: true`），界面显示“更早的对话已压缩为摘要”，可展开查看；
6. **失败语义**：用户取消 → 本轮 `cancelled`、历史不变；摘要失败（网络 / 厂商报错 / 返回空）→ 历史不变，发一条非致命的 `compact_failed` 警告，本轮继续，同一轮内不再重试；摘要比原文还大 → 不替换。
7. **代价**：压缩会改写历史，被摘要的原文不再保留；摘要调用在用量统计里记为 `compact` 来源。

还没做：手动“立即压缩”按钮、用厂商返回的 `input_tokens` 校准估算、Anthropic 服务端压缩（beta）、按模型分别配置触发比例。

### MCP 接入 ✅（tools 能力）

已实现：[`internal/mcp`](../internal/mcp/doc.go) 中的 stdio 与 Streamable HTTP 客户端、`initialize` 握手、`tools/list`（分页）、`tools/call`、响应服务器的 `ping`；
`mcp.Manager` 按设置在后台连接、注册/移除工具，进程退出时自动移除工具并在设置页显示原因。配置在 **设置 → MCP**（保存在 settings.json 的 `mcp_servers`）。

还没做：resources、prompts、sampling；服务器推送流（GET）和 `notifications/tools/list_changed`（目前工具列表变化需要点"重新连接"）；
OAuth 认证（目前只能在请求头里填令牌）；把服务器的 `instructions` 拼进系统提示词。

### 插件系统

接口：[`internal/plugin/plugin.go`](../internal/plugin/plugin.go)。

| 扩展 | 接入点 |
| --- | --- |
| Skill | 系统提示词里列出技能名和简介；提供 `read_skill` 工具按需加载正文 |
| SlashCommand | 前端/CLI 解析 `/xxx`，调用新的绑定方法 `ExpandCommand(name, args)`，结果作为用户消息发送 |
| Hook | `AgentConfig` 增加 `Hooks`；`runTool` 在权限裁决前调用 `PreToolUse`，执行后调用 `PostToolUse`；`Engine.Send` 开头调用 `UserPromptSubmit` |
| StatusLine | ✅ 已实现为"运行一条命令"（`plugin.CommandStatusLine`），由前端在状态变化时调用 `RunStatusLine`，没有新增事件 |

插件加载：先支持"目录 + plugin.toml + 外部可执行文件（JSON over stdio）"，这样插件可以用任何语言编写，也避免 Go plugin 的平台限制。

## 其他改进方向

- 工具：`edit_file`（基于字符串替换的局部修改，比整体覆盖更安全）、`glob`、`grep`；read 风险工具并行执行
- 权限：按命令前缀/路径的细粒度规则；记住"总是允许"到配置文件
- 密钥：从 settings.json 明文改为存入系统钥匙串（macOS Keychain / Windows Credential Manager / Linux Secret Service，例如用 `github.com/zalando/go-keyring`），settings.json 只保存引用
- 厂商：Gemini 原生协议；OpenAI Responses API；请求重试与 429 退避
- 会话：标题由模型生成；会话搜索；导出 Markdown
- 前端：长会话虚拟列表；高频 delta 批量合并；代码块高亮；diff 视图
- 事件持久化：用订阅者把事件写入 `events` 表，实现完整回放
- 迁移到 Wails v3（稳定后）：只影响 `internal/app` 和 `main.go`
