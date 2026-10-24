# 路线图与接口预留情况

## 第一阶段（当前）✅

- 单会话流式对话，切换厂商与模型（OpenAI 兼容 + Anthropic 原生 + fake）
- agent 循环：工具调用 → 执行 → 回传 → 继续，最大步数限制，取消
- 内置工具：read_file / write_file / list_dir / shell
- 权限：write/exec 前 `permission_request`，前端弹窗确认；`readonly` / `default` / `accept_edits` / `auto` 四档模式，可在状态栏或 CLI `/mode` 随时切换；本会话总是允许
- 前端：消息流、可折叠思考卡片、工具调用卡片、权限弹窗、状态栏（厂商/模型/token）、会话列表
- 会话保存与恢复（SQLite，纯 Go）
- CLI 调试入口；浏览器演示模式
- 设置页：界面上配置厂商（Base URL + API Key + 模型列表），获取模型列表、测试连接，修改立即生效，记住上次选择
- 设置页（续）：权限规则、工具开关、MCP 服务器、自定义状态栏脚本、API Key 存系统钥匙串、外观（主题/字号/宽度/思考卡片/发送键）、中英文界面、由模型生成会话标题

## 预留能力一览

| 能力 | 事件 | 后端接口 | 前端 | 还差什么 |
| --- | --- | --- | --- | --- |
| 子 agent 调度与可视化树 | ✅ `subagent_start/end`，信封 `agent_id`/`parent_id` | ✅ `SubagentDefinition`、`SubagentRegistry`；⬜ `SubagentRunner` | ✅ reducer 建树；`ActivityPanel` 显示 | Runner 实现 + `task` 工具 |
| 后台任务 | ✅ `task_start/log/end` | ⬜ `TaskManager` 接口 | ✅ reducer + 面板 | TaskManager 实现 + 3 个工具 |
| 上下文压缩 | ✅ `context_compacted` | ✅ `Compactor` 接口，已在每步前调用（no-op） | ✅ 显示提示 | 一个真正的 Compactor |
| MCP 接入 | — | ✅ stdio / Streamable HTTP 客户端、`mcp.Manager` | ✅ 设置 → MCP | resources / prompts、服务器推送（工具列表变化通知） |
| 插件：状态栏脚本 | — | ✅ `plugin.CommandStatusLine` | ✅ 状态栏"脚本输出"项 | — |
| 插件：技能 / 斜杠命令 / 钩子 | — | ⬜ `internal/plugin` 接口 | ⬜ | 加载器 + 各接入点 |

✅ 已有　⬜ 只有接口 / 未做

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

### 上下文压缩

接口：[`internal/core/compact.go`](../internal/core/compact.go)，已在 `Agent.prepareContext` 中每步调用。

一个简单可行的实现：
1. 用上一次调用的 `input_tokens` 估计当前上下文大小，超过窗口的 70% 触发；
2. 保留最近 N 轮原文，更早的部分交给同一个 Provider 生成摘要；
3. 用"一条包含摘要的 user 消息 + 最近 N 轮"替换历史；**不要把 `tool_use` 和它的 `tool_result` 拆开**；
4. 返回 `CompactResult{Changed: true, ...}`，内核会替换会话消息并发出 `context_compacted`。

对 Anthropic，也可以改用服务端压缩（beta），由适配器处理返回的压缩块——此时 Compactor 保持 no-op。

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
