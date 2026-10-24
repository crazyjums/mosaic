# 统一事件协议（v1）

事件协议是整个项目最核心的契约：**内核的一切可见变化都以事件的形式输出**，界面（桌面前端、CLI、测试）只消费事件。

- Go 权威定义：[`internal/event/event.go`](../internal/event/event.go)（信封）、[`internal/event/payloads.go`](../internal/event/payloads.go)（载荷）
- TypeScript 镜像：[`frontend/src/protocol/events.ts`](../frontend/src/protocol/events.ts)
- 真实样例：[`frontend/src/bridge/demo-events.jsonl`](../frontend/src/bridge/demo-events.jsonl)（用 CLI `-json` 录制的完整一轮）

## 1. 传输

| 场景 | 方式 |
| --- | --- |
| 进程内 | `event.Bus.Publish(event.Event)`，订阅者同步回调，保证顺序 |
| 桌面端 | Wails 事件 `agent:event`，载荷为一个事件的 JSON |
| CLI | `-json` 时每行一个事件（JSONL） |

所有事件走**同一个通道**，前端按 `type` 分发。不为每种类型单开 Wails 事件，是因为不同事件名之间 Wails 不保证顺序。

## 2. 版本规则

信封里的 `v` 是协议版本，当前为 `1`。

- **不升版本**：新增事件类型、给载荷新增可选字段。消费方必须忽略不认识的 `type` 和字段。
- **升版本**：删除或重命名字段、改变已有字段的含义或类型。

前端发现 `v` 不等于自己的 `PROTOCOL_VERSION` 时，在消息流中显示一次提示，但继续尽力渲染。

## 3. 信封

```json
{
  "v": 1,
  "type": "tool_call",
  "id": "ev_307eab4445c346f8",
  "seq": 16,
  "session_id": "sess_0a67b1055433a1ee",
  "agent_id": "main",
  "parent_id": "",
  "message_id": "msg_b347abb258b45b87",
  "ts": "2026-10-24T08:55:25.978285432Z",
  "data": { "...": "按 type 不同而不同" }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `v` | int | 协议版本 |
| `type` | string | 事件类型，见第 4 节 |
| `id` | string | 事件唯一 ID（`ev_` 前缀），用于去重 |
| `seq` | int | 同一个 Bus 内单调递增的序号，用于排查乱序 |
| `session_id` | string | 所属会话。前端只处理当前会话的事件 |
| `agent_id` | string | 产生事件的 agent。主 agent 固定为 `"main"` |
| `parent_id` | string? | 父 agent 的 `agent_id`。主 agent 为空。用 `agent_id` + `parent_id` 可以把任意事件挂到 agent 树上 |
| `message_id` | string? | 所属 assistant 消息（一次模型调用）。消息内的事件（delta、tool_call、image）都带；轮级事件为空 |
| `ts` | string | RFC 3339 时间戳 |
| `data` | object | 载荷 |

**概念层级**：会话（session）⊃ 轮（turn，一次用户输入到 agent 停下）⊃ 消息/步（message/step，一次模型调用）⊃ 内容块（思考、文本、工具调用、图片）。

## 4. 事件类型

### 4.1 轮级事件

#### `turn_start`

开始处理一次用户输入。前端据此锁定输入框。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `turn_id` | string | 本轮 ID |
| `provider` | string | 厂商配置档名称 |
| `model` | string | 模型 |
| `replay` | bool? | 为 true 表示会话恢复时的重放 |

```json
{"turn_id": "turn_5161fe2dc248c28d", "provider": "fake", "model": "demo"}
```

#### `user_message`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `text` | string | 用户输入 |

```json
{"text": "帮我做个演示"}
```

#### `turn_end`

一轮结束。**所有退出路径都会发出 `turn_end`**（正常、出错、取消、超步数），前端可以放心依赖它解锁输入框。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `turn_id` | string | 本轮 ID |
| `reason` | string | `completed` / `max_steps` / `cancelled` / `error` |
| `steps` | int | 本轮调用模型的次数 |
| `usage` | Usage | 本轮合计用量 |
| `session_usage` | Usage | 会话累计用量（状态栏显示它） |
| `replay` | bool? | 重放标记 |

```json
{"turn_id": "turn_5161fe2dc248c28d", "reason": "completed", "steps": 4,
 "usage": {"input_tokens": 887, "output_tokens": 225},
 "session_usage": {"input_tokens": 887, "output_tokens": 225}}
```

`Usage` 结构：`{"input_tokens", "output_tokens", "cache_read_tokens"?, "cache_write_tokens"?}`。

### 4.2 消息级事件

#### `message_start`

一次模型调用开始。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `provider` | string | 厂商配置档 |
| `model` | string | 模型 |
| `step` | int | 本轮第几次调用，从 1 开始 |

```json
{"provider": "fake", "model": "demo", "step": 1}
```

#### `thinking_delta`

思考内容增量。厂商不返回思考时**不会出现**，前端因此不显示思考卡片（降级）。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `text` | string | 思考片段 |
| `redacted` | bool? | 为 true 表示厂商返回了加密思考（Anthropic `redacted_thinking`），`text` 为空 |

```json
{"text": "用户的请求是"}
```

> Anthropic 的思考签名（signature）不进入事件——它对界面没有意义，只保存在会话历史里用于回传给模型。

#### `text_delta`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `text` | string | 正文片段（Markdown） |

```json
{"text": "我先看一下工"}
```

#### `image`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `media_type` | string | 如 `image/png` |
| `data` | string? | base64，不含 `data:` 前缀 |
| `url` | string? | 预留：外部地址 |
| `alt` | string? | 替代文本 |

```json
{"media_type": "image/png", "data": "iVBORw0KGgoAAAANSUhEUgAA..."}
```

#### `message_end`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `stop_reason` | string | `end_turn` / `tool_use` / `max_tokens` / `refusal` / `other` / `cancelled` / `error` |
| `usage` | Usage | 本次调用用量 |

```json
{"stop_reason": "tool_use", "usage": {"input_tokens": 124, "output_tokens": 49}}
```

### 4.3 工具与权限

一次工具调用的完整事件序列：

```
tool_call ──(message_end)──┬─────────────────────────────────────────────▶ tool_result   （read：直接执行）
                           └▶ permission_request ─▶ permission_resolved ─▶ tool_result   （write/exec：需确认）
```

#### `tool_call`

模型请求调用工具。参数已完整（适配器负责拼接流式分片）。此时工具尚未执行——所有工具在 `message_end` 之后依次执行。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `call_id` | string | 调用 ID，后续事件用它关联 |
| `name` | string | 工具名 |
| `input` | object | 参数 |
| `risk` | string | `read` / `write` / `exec`；未知工具为空 |

```json
{"call_id": "call_eab12343f8d5", "name": "list_dir", "input": {"path": "."}, "risk": "read"}
```

#### `permission_request`

请求用户确认。**agent 循环此时阻塞**，直到调用 `RespondPermission(request_id, decision)`，或本轮被取消。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `request_id` | string | 确认请求 ID，回复时使用 |
| `call_id` | string | 对应的工具调用 |
| `tool` | string | 工具名 |
| `risk` | string | 风险等级 |
| `summary` | string | 工具生成的人类可读摘要 |
| `input` | object | 参数 |

```json
{"request_id": "perm_599cbdb69644cbb3", "call_id": "call_1d06d4ae45de", "tool": "write_file",
 "risk": "write", "summary": "写入 agent-demo/hello.md（62 字节）",
 "input": {"path": "agent-demo/hello.md", "content": "# Hello from fake provider\n\n用户输入：帮我做个演示\n"}}
```

#### `permission_resolved`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `request_id` | string | |
| `call_id` | string | |
| `decision` | string | `allow` / `allow_session` / `deny` |
| `reason` | string? | 非用户主动拒绝时的原因，如 `cancelled` |

```json
{"request_id": "perm_599cbdb69644cbb3", "call_id": "call_1d06d4ae45de", "decision": "allow"}
```

#### `tool_result`

每个 `tool_call` 都**恰好对应一个** `tool_result`（包括未知工具、被拒绝、被取消的情况）。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `call_id` | string | |
| `name` | string | 工具名 |
| `output` | string | 输出（过长会被截断并注明） |
| `is_error` | bool | 失败、被拒绝、被取消时为 true |
| `duration_ms` | int | 工具执行耗时，不含等待用户确认的时间；未执行（被拒绝、未知工具）和重放时为 0 |

```json
{"call_id": "call_eab12343f8d5", "name": "list_dir", "output": "README.md\nsrc/", "is_error": false, "duration_ms": 0}
```

### 4.4 错误

#### `error`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `code` | string | `provider_error` / `max_steps` / `max_tokens` / `context_error` / `store_error` / `busy` / `send_failed` |
| `message` | string | 给人看的说明 |
| `fatal` | bool | true 表示本轮因此结束（随后会有 `turn_end`）；false 为警告 |

```json
{"code": "max_steps", "message": "已达到最大步数 20，agent 循环被停止。可以发送“继续”让它接着做。", "fatal": true}
```

### 4.5 会话

#### `session_updated`

会话的元数据变了。目前只在"由模型生成标题"时产生：第一轮结束后在后台生成，所以它会在 `turn_end` **之后**到达（可能晚几秒，期间用户可能已经发出下一条消息）。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `title` | string | 新标题 |

```json
{"title": "Go 并发入门"}
```

### 4.6 预留事件（第一阶段不产生，前端已能接收）

#### `subagent_start` / `subagent_end`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `agent_id` | string | 子 agent ID |
| `parent_id` | string | 父 agent ID（主 agent 为 `main`） |
| `name` | string | 子 agent 定义名（仅 start） |
| `task` | string | 派给它的任务（仅 start） |
| `status` | string | `completed` / `failed` / `cancelled`（仅 end） |
| `summary` | string? | 结果摘要（仅 end） |

```json
{"agent_id": "agent_7a1c", "parent_id": "main", "name": "explorer", "task": "找出所有 HTTP handler"}
```

子 agent 运行期间发出的所有事件，信封里 `agent_id` 为子 agent 的 ID、`parent_id` 为父 agent 的 ID，前端据此把它们归入树的对应节点。

#### `task_start` / `task_log` / `task_end`

| 事件 | 字段 |
| --- | --- |
| `task_start` | `task_id`, `title`, `kind`（如 `shell`） |
| `task_log` | `task_id`, `stream`（`stdout`/`stderr`/`info`）, `text` |
| `task_end` | `task_id`, `status`, `exit_code` |

```json
{"task_id": "task_1", "stream": "stdout", "text": "Listening on :5173\n"}
```

#### `context_compacted`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `before_tokens` | int | 压缩前估计 token 数 |
| `after_tokens` | int | 压缩后 |
| `removed_messages` | int | 被移除/替换的消息数 |
| `summary` | string? | 生成的摘要 |

```json
{"before_tokens": 182000, "after_tokens": 24000, "removed_messages": 37}
```

## 5. 一轮的典型事件序列

以 fake provider 的演示剧本为例（相邻的 delta 合并显示）：

```
turn_start
user_message
message_start(step=1) thinking_delta… text_delta… tool_call(list_dir)  message_end(tool_use)
tool_result(list_dir)
message_start(step=2) thinking_delta… text_delta… tool_call(write_file) message_end(tool_use)
permission_request → permission_resolved(allow) → tool_result(write_file)
message_start(step=3) thinking_delta… tool_call(shell) message_end(tool_use)
permission_request → permission_resolved(deny)  → tool_result(shell, is_error)
message_start(step=4) thinking_delta… text_delta… image message_end(end_turn)
turn_end(completed)
```

## 6. 会话恢复（重放）

`Engine.LoadSession` 把历史消息转换为事件重新发布（[`core.ReplayEvents`](../internal/core/replay.go)），前端用同一条渲染路径显示历史。
重放事件的 `turn_start` / `turn_end` 带 `replay: true`；历史中不存在的信息（工具耗时、权限确认过程、每条消息的用量）不会出现。

## 7. 修改协议的清单

1. 修改 `internal/event/payloads.go`（新类型要实现 `EventType()` 并在 `init()` 里 `register`）；
2. 在 `internal/event/event_test.go` 的样例里补一条（测试会检查数量是否一致）；
3. 修改 `frontend/src/protocol/events.ts` 的 `PayloadMap` 和 `KNOWN_TYPES`；
4. 在 `frontend/src/store/reducer.ts` 里处理它；
5. 更新本文档；破坏性修改时把两端的版本号 +1。
