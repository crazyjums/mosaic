# 子 agent 与钩子（hooks）：怎么做

状态：**已评审，按建议实施中**。S1（子 agent 调度器 + task 工具 + 用户目录）、S2（插件 agents 导入）、H1（钩子引擎：`internal/core/hooks.go` + `internal/hooks`）已实现；H2 进行中。评审结论：§4 的五个问题都按建议。

背景：插件重设计（[plugin-system-v3.md](plugin-system-v3.md)）之后，Claude Code / Codex 格式插件里的 `agents/` 和 `hooks/` 会被**忽略并告知**。这份文档回答：怎样真正支持它们，哪些地方要和 Claude Code 不一样，以及为什么。

格式和语义依据 Claude Code 官方文档：子 agent（sub-agents）、钩子（hooks）、插件参考（plugins-reference）。

## 0. 先说结论

| | 子 agent | 钩子 |
| --- | --- | --- |
| 价值 | 高：把“查资料、跑测试、审代码”这类会产生大量中间输出的活派出去，主对话只拿到结论 | 中：格式化、拦截危险命令、提交前检查 |
| 风险 | 低：子 agent 只是**另一段提示词 + 同一套工具和权限**，不执行任何新代码 | 高：钩子是**在你电脑上运行任意命令**，还能影响权限判断 |
| Mosaic 现状 | 定义、注册表、`subagent_start/end` 事件、前端的树都已有，**只差调度器** | 只有接口占位（`internal/plugin/plugin.go`），没有接线 |
| 建议顺序 | **先做** | 后做，而且比 Claude Code 更保守（见 §2.2） |

两者都**只作用于 Mosaic 内置的 agent 循环**。Codex、Claude Code、pi 引擎各有自己的循环：Claude Code 引擎跑的是你本机的 `claude`，它本来就会执行你自己配置的 Claude Code 钩子和 agents，Mosaic 不重复做一遍。

## 1. 子 agent

### 1.1 工作方式

主 agent 多一个工具 `task`（参数 `{agent, prompt}`；工具描述里列出所有可用的子 agent 及其 `description`）。调用时：

1. 用一份新的 `AgentConfig` 创建 `Agent`：新 `agent_id`、`parent_id` = 调用者、子 agent 自己的系统提示词、受限的工具表、同一个模型厂商（或映射后的模型）。`Agent` 本身无状态、依赖全部注入，所以**不需要改 `Agent`**（`internal/core/subagent.go` 开头的设计就是这么留的）。
2. 子 agent 在**独立的上下文**里跑：只看到自己的系统提示词和主 agent 写的任务，看不到主对话历史（与 Claude Code 一致）。
3. 事件照常发到同一条总线，信封里的 `agent_id` / `parent_id` 让前端挂到树上；开始、结束各发 `subagent_start` / `subagent_end`（事件类型已存在）。界面上，子 agent 的过程折叠在主对话里那张 `task` 工具卡片下面。
4. 只有子 agent 的**最终回复**作为 `tool_result` 回到主 agent。用量（token）计入这个会话。

### 1.2 关键取舍

- **工具**：默认继承主 agent 的全部工具，**减去 `task` 本身**（第一版不允许子 agent 再派子 agent；Claude Code 默认允许 3 层）。`tools` 是白名单、`disallowedTools` 是黑名单（先黑后白）。白名单一个都对不上时**拒绝加载这个子 agent 并说明原因**，不悄悄给它全部工具。
- **工具名映射**：Claude Code 的 agent 文件写的是 `Read, Grep, Bash` 这类名字，Mosaic 的内置工具叫 `read_file、search_content、shell`。需要一张映射表：

  | Claude Code | Mosaic |
  | --- | --- |
  | Read | read_file |
  | Write | write_file |
  | Edit / MultiEdit | edit_file |
  | Glob | find_files |
  | Grep | search_content |
  | LS | list_dir |
  | Bash | shell |
  | TodoWrite | todo_write（+ todo_read） |
  | `mcp__…` | 同名 MCP 工具 |
  | WebFetch / WebSearch / NotebookEdit / Agent / Skill 等 | 没有对应：忽略并在详情里列出 |

- **权限**：和主 agent **同一套档位、规则和确认弹窗**；弹窗上标明是哪个子 agent 发起的。子 agent 不能有比主对话更宽的权限。**插件带来的子 agent 的 `permissionMode`、`hooks`、`mcpServers` 一律忽略**——这和 Claude Code 对插件 agent 的做法一致，理由也一样：一个第三方插件不该通过 agent 定义给自己放宽权限或塞进可执行组件。
- **模型**：`inherit` 或不写 → 和主对话相同。写 `sonnet` / `opus` / `haiku` 这类别名时，只在当前厂商配置档里有对应模型才换，否则沿用主对话的模型并在详情里提示。
- **`maxTurns`** → 子 agent 的最大步数（默认取一个较小的值，比如 20）。到了上限，回复标注“未完成”交回主 agent。
- **取消**：用户点停止，子 agent 随主轮次一起停。
- **并发**：第一版**顺序执行**（Mosaic 现在的工具调用本来就是一个接一个）。并行和后台子 agent 以后再说。
- **持久化**：第一版**不单独保存子 agent 的完整过程**，会话历史里只有 `task` 调用和最终回复；重新打开会话时，子 agent 的中间过程不再显示。这样不需要改存储格式，代价是回看不到细节。（Claude Code 是另存一份 transcript。）

### 1.3 子 agent 从哪来

1. **插件**：Claude Code 格式插件里的 `agents/*.md`（YAML frontmatter + 正文作为系统提示词，递归扫描），命名为 `插件id:名字`。装上就可用，详情页列出。这会把现在“子 agent 不会生效”的提示去掉。
2. **用户自己的**：`~/.myllmapp/agents/*.md`，同样格式，设置里能看到列表。
3. **工作区里的 `.claude/agents/`**：Claude Code 会自动加载。但它来自你打开的仓库，可能不是你写的（提示词注入的风险）。**建议默认不加载**，在工作区里按一次“信任这个工作区的子 agent”之后才加载。

### 1.4 验收

- 用演示厂商（fake provider）跑完整一轮：主 agent 调 `task` → `subagent_start` → 子 agent 的工具调用走权限确认 → `subagent_end` → 主 agent 收到最终回复。
- 工具白名单 / 黑名单 / 映射 / 全部对不上时拒绝；子 agent 拿不到 `task`；插件 agent 的 `permissionMode` 被忽略；取消能停住子 agent。
- 前端：子 agent 的过程折叠在 `task` 卡片下面，树正确。

## 2. 钩子

### 2.1 和 Claude Code 兼容的部分

- **配置格式**：`hooks/hooks.json` 或清单里的 `hooks`：事件 → `matcher` → `hooks[]`。`matcher` 规则照搬（空 / `*` 匹配全部；只含字母数字和 `|` `,` 的是精确匹配列表；其余是正则）。
- **第一版支持的事件**：`PreToolUse`、`PostToolUse`、`UserPromptSubmit`、`Stop`（这一轮结束）。子 agent 做完之后再加 `SubagentStart` / `SubagentStop`。其余事件（会话开始/结束、压缩、权限请求、MCP 引导等）不支持，列为“不会生效”。
- **处理器类型**：只支持 `command`。`http`、`prompt`、`agent`、`mcp_tool` 不支持，列为“不会生效”。exec 形式（有 `args`，不经过 shell）优先；shell 形式在没有 `sh` 的 Windows 上不支持并提示。
- **输入**：标准输入一份 JSON：`session_id`、`cwd`、`hook_event_name`、`permission_mode`（映射成 Claude Code 的取值）、`tool_name`、`tool_input`、`tool_use_id`、`tool_response`、`prompt`、`last_assistant_message`。变量 `${CLAUDE_PLUGIN_ROOT}` / `${CLAUDE_PLUGIN_DATA}` / `${CLAUDE_PROJECT_DIR}` 换成插件目录 / 插件数据目录 / 工作区。
- **输出**：退出码 0 = 成功，2 = 阻止（stderr 是原因），其他 = 不阻止的错误。标准输出的 JSON 支持 `continue`、`stopReason`、`decision: "block"` + `reason`、`hookSpecificOutput.permissionDecision`（`deny` / `ask`）、`permissionDecisionReason`、`updatedInput`、`additionalContext`。

### 2.2 比 Claude Code 更保守的地方（为什么）

钩子是在你电脑上运行的任意命令，而且站在权限系统旁边。Mosaic 的原则是：**钩子只能让事情更严，不能更松**。

1. **不能放行**：`permissionDecision: "allow"` **当作没表态**，该弹的确认照样弹。Claude Code 允许钩子直接放行，那等于给插件一个绕过确认弹窗的入口；Mosaic 不给。`deny` 和 `ask` 照办。
2. **改参数在确认之前**：`updatedInput` 在权限判断和确认弹窗**之前**生效，你在弹窗里看到的就是改过之后的参数，不会“你批准 A、实际执行 B”。
3. **逐条批准**：安装确认里把每条钩子的触发事件、匹配条件和**原样命令**都列出来，**每条单独勾选，默认不勾**。插件更新后钩子有变化，需要重新批准。插件详情里可以单独关掉某条钩子；设置里有一个“停用所有钩子”的总开关。
4. **看不到别人的私密内容**：标记为私密（`Sensitive`）的工具（比如飞书的读文档），它的输出**不传给**任何钩子的 `tool_response`——一个插件的钩子不该读到另一个插件拿到的私密内容。
5. **注入的上下文要有标注、有上限**：`additionalContext` 和 `UserPromptSubmit` 的输出会作为“来自插件 X 的钩子”的标注文字放进对话，最多 2000 字；不支持 `updatedToolOutput`（改写工具结果），因为它能悄悄改掉模型看到的事实。
6. **环境和时限**：和插件的 MCP 服务一样只拿白名单环境变量（不含你的 API Key）；默认时限 30 秒（Claude Code 是 600 秒），`UserPromptSubmit` 10 秒；超时不阻止（`onFailure: "block"` 的钩子在 `PreToolUse` 上超时会阻止——这是更严）。输出最多 10000 字。
7. **看得见**：每次钩子运行都发一个事件（哪个插件、哪个事件、结论、耗时），显示在对应的工具卡片或这一轮的活动里。被钩子阻止时，卡片上写明是哪个插件的哪条钩子、原因是什么。

### 2.3 钩子从哪来

第一版只来自**插件**（逐条批准）。“用户自己在设置里写钩子”可以以后加，到时规则相同。

### 2.4 验收

- 各事件的输入 JSON 字段；退出码 0 / 2 / 其他；JSON 输出各字段；`allow` 被忽略、`deny` / `ask` 生效；`updatedInput` 在确认之前生效、弹窗显示改后的参数；私密工具的输出不传给钩子；`additionalContext` 有标注和上限；超时与 `onFailure`。
- 没批准的钩子不运行；插件更新后钩子变化需要重新批准；总开关一关全部不运行；停用插件后不运行。
- Windows：exec 形式能跑；shell 形式在没有 `sh` 时给出明确提示。

## 3. 分阶段（每个都是独立 PR）

| 阶段 | 内容 | 验收要点 |
| --- | --- | --- |
| **S1** | 子 agent 调度器 + `task` 工具 + 用户目录 `~/.myllmapp/agents/` + 前端折叠显示 | 演示厂商跑通一轮；工具受限；权限照常确认；取消能停住 |
| **S2** | 插件 `agents/*.md` 导入（工具名映射、忽略插件 agent 的 permissionMode/hooks/mcpServers）、详情页列出 | Claude Code 格式插件的 agent 可用；“不会生效”里不再有 agents |
| **H1** | 钩子引擎：4 个事件、`command` 处理器、输入输出协议、“只能更严”的规则、事件可见 | 上面 §2.4 的协议和安全用例 |
| **H2** | 插件钩子导入 + 安装时逐条批准 + 详情页开关 + 总开关；加 `SubagentStart/Stop` | 没批准不运行；更新后重新批准 |

## 4. 需要你决定

1. **顺序**：先子 agent（S1、S2），再钩子（H1、H2）。可以吗？
2. **子 agent 嵌套**：第一版只允许一层（子 agent 不能再派子 agent）。可以吗？
3. **工作区里的 `.claude/agents/`**：默认不加载、按工作区手动信任之后才加载。可以吗？
4. **钩子不能放行**：钩子返回 `allow` 时仍然弹确认（和 Claude Code 不同）。可以吗？
5. **子 agent 过程不单独保存**：重新打开会话时只看得到最终回复。可以接受吗？还是第一版就要保存完整过程？
