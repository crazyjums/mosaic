# 技术方案（第一阶段）

> 本文是开工前写下的总体方案，回答"做什么、用什么、怎么分层、关键取舍是什么"。
> 各模块的细节设计在 `docs/design/`，事件协议的权威定义在 `docs/event-protocol.md`。
> 编码过程中如有偏离，以代码和后续文档为准，并在本文末尾"变更记录"里注明。

## 1. 目标与非目标

**目标**

1. 自己实现 agent harness：agent 循环、上下文、工具调用、权限确认，全部自己写，不依赖任何 agent SDK。
2. 多厂商：OpenAI 兼容协议（覆盖 OpenAI / DeepSeek / Qwen / Moonshot / Ollama 等）+ Anthropic 原生协议（保留思考块和签名）。
3. 可视化：思考过程、工具调用、权限确认、图像、token 用量；为子 agent 树、后台任务预留事件。
4. 学习项目：可读性 > 功能数量。每个包有 `doc.go`，关键流程有中文步骤注释。

**非目标（第一阶段不做）**

- 子 agent 实际调度、后台任务执行、上下文压缩算法、MCP 客户端、插件系统 —— 只定义接口和事件，写清楚接入方式。
- 多会话并发运行（一次只跑一个会话，但可以切换和恢复历史会话）。
- 官方厂商 SDK：两个协议都用 `net/http` + SSE 手写，目的是让协议细节可见、可调试。

## 2. 技术选型

| 领域 | 选择 | 理由 |
| --- | --- | --- |
| 桌面框架 | **Wails v2（v2.17.0）** | 截至 2026-10，v2 的最新 tag 是 v2.17.0（稳定版），v3 仍是 `v3.0.0-beta.34`。v3 的 API（多窗口、服务注册方式）在 beta 期间仍有破坏性变化，学习项目最怕"教程和代码对不上"，所以选 v2。内核与 Wails 完全解耦，将来迁移 v3 只需重写 `internal/app` 和 `main.go`。 |
| 后端语言 | Go 1.26 | `modernc.org/sqlite` 最新版要求 Go 1.26；本机 Go 更旧时 `GOTOOLCHAIN=auto`（默认值）会自动下载。 |
| SQLite 驱动 | **modernc.org/sqlite** | 纯 Go（C 代码机器翻译为 Go），无需 CGO，交叉编译和 CI 都简单。代价：比 `mattn/go-sqlite3` 慢一些、二进制更大，对聊天记录这种量级无影响。 |
| 配置格式 | **TOML**（`github.com/BurntSushi/toml`） | 支持注释（`config.example.toml` 可以自带说明），比 YAML 少缩进陷阱；JSON 不能写注释，被排除。 |
| 前端 | React 19 + TypeScript 7 + Vite 8 + Tailwind CSS v4 + Zustand 5 | 按需求指定，均取当前稳定版。Tailwind v4 用官方 Vite 插件，不需要 PostCSS 配置文件。 |
| 前端测试 | Vitest | 只测"事件 → 界面状态"的纯函数 reducer，这是前端最容易写错的部分。 |

## 3. 分层与依赖方向

```
            ┌─────────────────────────────────────────────┐
  入口层     │ main.go (Wails)   cmd/cli     internal/app   │
            └───────────────┬─────────────────────────────┘
                            │ 只依赖下层
            ┌───────────────▼─────────────────────────────┐
  装配层     │ internal/bootstrap  internal/config          │
            └───────────────┬─────────────────────────────┘
            ┌───────────────▼─────────────────────────────┐
  适配层     │ provider/openai  provider/anthropic          │
            │ provider/fake    tool/builtin   store        │
            └───────────────┬─────────────────────────────┘
            ┌───────────────▼─────────────────────────────┐
  内核层     │ internal/core （agent 循环、会话、引擎）      │
            └───────────────┬─────────────────────────────┘
            ┌───────────────▼─────────────────────────────┐
  契约层     │ event   provider(接口)   tool(接口)  permission│
            └─────────────────────────────────────────────┘
```

规则：

- **依赖只能向内（向下）**。契约层之间尽量不互相依赖（唯一例外：`tool` 引用 `permission.Risk` 来声明风险等级）。
- `internal/core` 只依赖契约层，**不导入 Wails、不导入任何具体厂商子包、不导入 store**。持久化通过 core 自己定义的 `SessionStore` 接口注入（依赖倒置）。
- `internal/app` 只做"Go 方法 ↔ 前端调用"和"事件总线 → Wails 事件"的转发，不写业务逻辑。业务编排放在 `core.Engine`，CLI 和桌面端共用。

> 取舍：把 `Provider` 接口放在 `internal/provider`（父包），具体厂商放子包。考虑过把接口放进 `core`（经典端口-适配器），
> 但那样 `store`、`fake` 等包要为了一个消息结构依赖整个内核；把"厂商中立的消息模型 + Provider 接口"做成独立契约包更直观。

## 4. 核心契约

### 4.1 统一事件流（最重要）

- 所有对外可见的变化都是一个 `event.Event`：信封（版本号 `v`、`id`、`seq`、`type`、`session_id`、`agent_id`、`parent_id`、`message_id`、`ts`）+ 按类型区分的 `data`。
- 事件类型：`turn_start`、`user_message`、`message_start`、`text_delta`、`thinking_delta`、`tool_call`、`tool_result`、`image`、`permission_request`、`permission_resolved`、`subagent_start/subagent_end`、`task_start/task_log/task_end`、`context_compacted`、`usage`（在 message_end 中携带）、`error`、`message_end`、`turn_end`。
  其中 `turn_*`、`user_message`、`permission_resolved` 是在需求列表之外补充的：前端需要知道"一整轮什么时候结束"（解锁输入框）、"权限确认什么时候结束"（关闭弹窗、工具卡片进入运行态），以及会话恢复时能用同一条事件路径渲染用户消息。
- 前端只消费事件，不读厂商响应；会话恢复时后端把历史消息"重放"成同一套事件，前端只有一条渲染路径。

### 4.2 Provider（厂商适配）

```go
type Provider interface {
    Name() string
    Stream(ctx context.Context, req Request, onDelta func(Delta)) error
}
```

- 厂商只负责"把自家协议翻译成少量中立的 `Delta`"（文本、思考、思考签名、完整工具调用、图像、用量、停止原因）。
- **由内核的累加器（accumulator）把 Delta 组装成最终消息**，而不是每个厂商自己组装：保证"存进历史的消息"和"界面看到的流"来源一致，也避免每个厂商重复写组装逻辑。
- 工具调用参数的分片拼接（OpenAI 按 index、Anthropic 按 content block）属于协议细节，留在厂商内部，拼好后发一个完整的 `tool_call` Delta。
- 中立消息模型采用"内容块"（content blocks）结构：`text / thinking / redacted_thinking / tool_use / tool_result / image`。它是 Anthropic 模型的超集形态；OpenAI 适配器负责把 `tool_result` 拆成 `role: tool` 消息、丢弃思考块。
- 厂商不返回思考内容时，就不会产生 `thinking_delta`，前端自然不显示思考卡片（降级）。

### 4.3 Tool（工具）

```go
type Tool interface {
    Spec() Spec                       // 名称、描述、JSON Schema、风险等级
    Execute(ctx context.Context, call Call) Result
}
```

- 风险等级 `read / write / exec` 由工具自己声明；是否需要确认由 `permission.Policy` 决定，工具本身不关心 UI。
- 工具通过 `tool.Registry` 注册；内置工具在 `internal/tool/builtin`，每个工具一个文件。
- 所有文件类工具限定在工作区根目录内（拒绝 `..` 逃逸），shell 工具有超时和输出截断。

### 4.4 Permission（权限）

- `Policy`：按风险等级给出 `allow / ask / deny`，并记住"本会话总是允许某工具"。
- `Broker`：挂起等待的请求表。内核调用 `Broker.Request(ctx, req, announce)`：先登记、再发 `permission_request` 事件、再阻塞等待；前端（或 CLI）调用 `Engine.RespondPermission(id, decision)` 唤醒。先登记后通知，避免"回复比登记先到"的竞态。

### 4.5 Core（内核）

- `Agent.Run`：一次用户输入 → 多步（step）模型调用，直到模型不再调用工具或达到 `MaxSteps`。
- `Engine`：命令接口（新建/加载/列出会话、发送、取消、回复权限、切换厂商模型、查询状态），持有事件总线。CLI 和 Wails 绑定层都只和 `Engine` 打交道。
- 预留扩展点（只定义接口）：`Compactor`（上下文压缩，每步调用；后来已实现为 `SummaryCompactor`）、`SubagentRegistry`/`SubagentDefinition`、`TaskManager`。

### 4.6 Store（持久化）

- `core.SessionStore` 接口由 `internal/store` 用 SQLite 实现；另有内存实现用于测试。
- 表：`sessions`（元数据、用量）+ `messages`（按序号存中立消息 JSON）。每轮结束整体重写该会话的消息（简单可靠；后续再改增量追加）。

## 5. 一次请求的数据流（概要）

1. 前端 `Composer` 调用 Wails 绑定 `App.Send(text)`。
2. `App.Send` 起 goroutine 调用 `Engine.Send` → `Agent.Run`。
3. `Agent.Run` 发 `turn_start`、`user_message`，构造 `provider.Request`，调用 `Provider.Stream`。
4. 厂商适配器解析 SSE → `Delta` 回调 → 内核累加器 + 翻译为 `text_delta` / `thinking_delta` / `tool_call` 事件 → `event.Bus`。
5. `Bus` 的订阅者：`App` 用 `runtime.EventsEmit("agent:event", ev)` 转给前端；CLI 订阅者打印到终端。
6. 工具调用：需要确认 → `permission_request` → 弹窗 → `App.RespondPermission` → 执行 → `tool_result` → 下一步模型调用。
7. 结束：`message_end`（含用量）→ `turn_end` → 保存会话。
8. 前端 `useChatStore.apply(event)` 用纯函数 reducer 更新状态，React 组件渲染。

详细时序图见 `docs/architecture.md`。

## 6. 可测试性

- `provider/fake`：按脚本（Go 结构体或 JSON 文件）逐步返回 Delta，可配置每个 Delta 的延迟用于演示；内置一个"思考 → 列目录 → 写文件（需确认）→ 回答"的演示脚本。
- 内核测试全部用 fake provider + 临时目录，不需要网络和密钥。
- OpenAI / Anthropic 适配器用 `httptest.Server` 回放录制好的 SSE 文本做单元测试。
- CLI 支持 `--json` 输出原始事件（JSONL），既用于调试协议，也用于生成前端浏览器演示模式的回放数据。

## 7. 桌面端构建的约束

云端环境没有 WebKitGTK，Wails 桌面端无法在此构建。处理方式：

- 根目录 `main.go` 带构建标签 `//go:build desktop || dev || production || bindings`（Wails CLI 构建时会带这些标签），
  因此 `go build ./... / go vet ./... / go test ./...` 不会编译它，也不需要 `frontend/dist` 已存在。
- 内核、CLI、测试、前端（`npm run build` / 类型检查 / Vitest）都能独立构建运行。
- 前端在浏览器里单独运行时（没有 Wails 注入的 `window.go`），自动切换到"演示桥接"，回放 fake provider 录制的事件。

## 8. 密钥安全

- 密钥来源优先级：环境变量（配置里写 `api_key_env = "XXX_API_KEY"`）> 配置文件里的 `api_key`。
- 仓库只提交 `config.example.toml`；`config.toml`、`.env`、数据目录全部在 `.gitignore`。
- 绑定层返回给前端的配置视图里，密钥只返回"是否已配置"，不返回明文。

## 9. 风险与应对

| 风险 | 应对 |
| --- | --- |
| 云端无法验证桌面端 | 绑定层足够薄；列出本地验证步骤 |
| 各家 OpenAI 兼容实现对思考字段不统一（`reasoning_content` / `reasoning`） | 适配器两个字段都识别；都没有就降级 |
| Anthropic 思考块必须原样回传（含签名） | 中立消息模型保留 `signature` 和 `redacted_thinking`；有测试覆盖 |
| 流式事件过多导致前端卡顿 | 第一阶段直接逐条处理；如有需要再在 App 层做批量合并（文档中说明） |

## 变更记录

- 初版：开工前方案。
- 实现阶段：
  - 前端版本落定为 React 19.3 / TypeScript 7.0 / Vite 8.3 / Tailwind 4.3 / Zustand 5.0（初稿写的是 React 18）。
  - 新增 `internal/archtest`（仓库级守卫测试）与 `provider/sse`（共用 SSE 解析器）两个小包。
  - 前端浏览器演示模式改为回放 CLI 录制的真实事件流（`frontend/src/bridge/demo-events.jsonl`），而不是另写一套 TS 版 fake。
