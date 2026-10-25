# 设计：厂商适配（internal/provider）

## 目标

1. 内核不认识任何具体厂商；
2. 新增一个 OpenAI 兼容厂商 = 只改配置；新增一种协议 = 新建一个包；
3. 厂商之间的差异（思考、工具调用分片、用量字段）全部封装在适配器内部。

## 结构

```
internal/provider/            契约：中立消息、Provider 接口、注册表、Catalog
├── sse/                      共用的 SSE 解析器
├── openai/                   OpenAI 兼容 Chat Completions
├── anthropic/                Anthropic Messages API（原生）
├── chatgpt/                  Responses API + ChatGPT 账号登录（套餐额度）
├── fake/                     剧本驱动的假厂商
└── all/                      空导入，注册以上全部
```

## 三个层次的概念

| 概念 | 例子 | 在哪定义 |
| --- | --- | --- |
| 适配器类型（adapter type） | `openai`、`anthropic`、`fake` | 代码：`provider.Register("openai", New)` |
| 配置档（profile） | `deepseek`、`qwen`、`ollama`（都是 `openai` 类型） | 配置文件 `[[providers]]` |
| 模型 | `deepseek-chat` | 配置档的 `models` 列表 |

`provider.Catalog` 为每个配置档创建一个实例。缺密钥等错误只让该配置档不可用（界面置灰并显示原因），不影响启动。

## 中立消息模型

```go
type Message struct { Role Role; Content []Block }
type Block struct {
    Type BlockType // text / thinking / redacted_thinking / tool_use / tool_result / image
    Text, Signature, Data string
    ToolCallID, ToolName string; ToolInput json.RawMessage; IsError bool
    MediaType, ImageData string
}
```

- 采用"内容块"结构（Anthropic 风格），因为它是超集：一条消息可以交错包含思考、文本、多个工具调用。
- 只有 `user` / `assistant` 两种角色，工具结果是 user 消息里的 `tool_result` 块。
- 扁平结构 + `Type` 判别，而不是每种块一个 Go 类型：序列化、存库、TS 类型都直接，打开数据库就能看懂。

## Provider 接口与 Delta

```go
type Provider interface {
    Name() string
    Stream(ctx context.Context, req Request, onDelta func(Delta)) error
}
```

`Delta` 只有 8 种：`text`、`thinking`、`thinking_signature`、`redacted_thinking`、`tool_call`、`image`、`usage`、`stop`。

约定：
- 工具调用参数**拼接完整后**才发一个 `tool_call` Delta；
- `usage` 可出现多次，以最后一次为准；
- 正常结束时最后发 `stop`；
- ctx 取消时尽快返回 `ctx.Err()`；
- 参数不是合法 JSON 时包装成 `{"_raw": "..."}`，保证下游序列化不出错，工具解析失败后模型会看到错误并纠正。

**为什么用回调**：channel 需要额外 goroutine 和关闭约定；`iter.Seq2` 对初学者不直观。回调 + 返回 error 最朴素：`Stream` 返回时调用一定结束了。

**为什么厂商不组装完整消息**：见 [内核循环](agent-loop.md#delta-的双重去向)。

## OpenAI 兼容适配器

协议：`POST {base_url}/chat/completions`，`stream: true`，`stream_options.include_usage: true`。

| 中立模型 | Chat Completions |
| --- | --- |
| `System` | 第一条 `role: system` 消息 |
| user 的 `tool_result` 块 | 独立的 `role: tool` 消息（每个结果一条，排在同一 user 消息的文本之前） |
| user 的 `image` 块 | `content` 数组里的 `image_url`（data URL） |
| assistant 的 `tool_use` 块 | `tool_calls[]`，参数是 **JSON 字符串** |
| assistant 的 `thinking` 块 | 丢弃（协议没有标准的回传字段） |

流式解析要点：
- 思考：`delta.reasoning_content`（DeepSeek、Qwen）或 `delta.reasoning`（OpenRouter 等）；都没有就没有思考 Delta → 界面降级。
- 工具调用：按 `index` 分片到达，第一片带 `id` 和 `name`，之后只有 `arguments` 片段；流结束后按 index 顺序发出。
- `finish_reason`：`stop`→`end_turn`，`tool_calls`→`tool_use`，`length`→`max_tokens`，`content_filter`→`refusal`。

选项：`max_tokens_field`（`max_tokens` 或 `max_completion_tokens`）、`allow_no_key`（本地 Ollama/vLLM）。

## Anthropic 原生适配器

协议：`POST {base_url}/v1/messages`，`stream: true`，头 `x-api-key`、`anthropic-version: 2023-06-01`。

**为什么需要原生协议而不是只用 OpenAI 兼容层**：思考块必须连同签名原样回传，否则多步工具调用时 API 会拒绝请求；OpenAI 兼容层没有这个字段。

请求：
- 默认 `thinking: {type: "adaptive", display: "summarized"}`。新模型默认不返回思考文本（只有空思考块），显式设置 `display: "summarized"` 才能在界面上看到思考过程。可通过 `options.thinking = "omit"` 关闭（用于不支持该参数的旧模型或代理）。
- 不发送 `temperature` / `budget_tokens` 等新模型已移除的参数。
- 可选 `output_config.effort`、服务端拒绝回退 `fallbacks: "default"`（需 beta 头 `server-side-fallback-2026-07-01`）。

SSE 事件 → Delta：

| SSE 事件 | 处理 |
| --- | --- |
| `message_start` | 记录输入用量（含缓存读写） |
| `content_block_start` | 记录块类型；`redacted_thinking` 立即发出 |
| `content_block_delta` | `text_delta`→text，`thinking_delta`→thinking，`signature_delta`→thinking_signature，`input_json_delta`→本地拼接 |
| `content_block_stop` | `tool_use` 块此时发出完整的 tool_call |
| `message_delta` | 停止原因、累计输出用量 |
| `message_stop` | 发出 usage + stop，返回 |
| `error` | 返回错误 |
| `ping` | 忽略 |
| 没有 `message_stop` 就断流 | 返回错误（连接被意外中断） |

回传历史时：没有签名的思考块（来自其他厂商或被中断的调用）、空文本块、assistant 里的图片都会被跳过，避免 400。

## ChatGPT 适配器（Responses API）

用 ChatGPT 账号登录后（登录见 [`internal/siwc`](../../internal/siwc/doc.go)），用套餐额度调用 `POST https://api.openai.com/v1/responses`。

- **认证**：`ProfileConfig.Auth`（`provider.TokenSource`）而不是 `APIKey`。访问令牌一小时过期，由 `siwc.Manager.Token` 按需刷新；适配器每次请求前取一次。settings 在组装配置档时把登录管理器注入给类型为 `chatgpt` 的配置档；
- **请求**：系统提示词放 `instructions`；中立消息拆成输入条目——用户文本 / 图片是 `message`（`input_text` / `input_image`），assistant 文本是 `message`（`output_text`），每个工具调用是 `function_call`，每个工具结果是 `function_call_output`；工具是普通的 `function` 工具；
- **套餐额度的限制**：必须 `store: false`、`stream: true`；不能发 `max_output_tokens`、`temperature` 等字段（所以 `req.MaxTokens` 被忽略）；不接受 system 角色的消息条目；
- **思考**：请求 `reasoning.summary = auto`，思考摘要显示在思考卡片里。思考不回传：`store=false` 时回传思考需要请求加密内容，套餐这条路未必支持，没有它模型照样能继续；
- **事件**：`response.output_text.delta` → 文本，`response.reasoning_summary_text.delta` → 思考，`response.output_item.done`（`function_call`，参数已完整）→ 工具调用，`response.completed` → 用量与停止原因；`response.failed` 和 `error` 事件转换成错误——额度用完可能在开始输出之后才到达；
- **错误码**：`subscription_sharing_usage_limit_exceeded`（额度用完）、`subscription_sharing_user_not_eligible`、`subscription_sharing_unsupported_capability`、`subscription_sharing_route_not_supported` 都翻译成中文说明，原始信息附在后面；
- **模型列表**：`GET /v1/models` 在这条路上返回 `{"models": [...]}`，元素形状没有写死，字符串或带 `id` / `slug` 的对象都接受。

为什么不复用 Codex CLI 的登录：那需要冒用 Codex 的 client_id 和请求头去调 `chatgpt.com` 的内部接口，违反条款，也随时可能失效。

## Fake 适配器

剧本（`Script`）由若干 `Step` 组成，每步可以有思考、文本、图片、工具调用、错误。第 N 次调用（从本轮用户发言算起）返回第 N 步，不管工具返回什么——这正是测试需要的确定性。`{{input}}` 占位符替换为用户输入（在 JSON 中会先转义）。

## 降级矩阵

| 能力 | OpenAI 兼容（无 reasoning 字段） | OpenAI 兼容（DeepSeek 等） | Anthropic | ChatGPT 账号 |
| --- | --- | --- | --- | --- |
| 思考卡片 | 不显示 | 显示 | 显示（摘要） | 显示（摘要） |
| 思考回传 | — | 丢弃 | 带签名回传 | 丢弃 |
| 缓存用量 | `cached_tokens`（如有） | 同左 | 读 / 写都有 | `cached_tokens` |
| 图片输出 | 不支持 | 不支持 | 不支持（协议本身） | 不支持 |
| 单次输出上限 | 支持 | 支持 | 支持 | 不支持（套餐额度的限制） |

## 测试

各适配器用 `httptest.Server` 回放录制的 SSE 文本，断言 Delta 序列、请求体字段、错误处理，不依赖网络和密钥。
