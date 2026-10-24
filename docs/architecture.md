# 整体架构

## 1. 一句话

**内核（agent 循环）通过"命令 + 事件"对外，界面只是事件的消费者。** 桌面端和 CLI 是同一个内核的两种界面。

## 2. 分层图

```mermaid
flowchart TB
    subgraph Entry["入口层"]
        MAIN["main.go<br/>(Wails 桌面端)"]
        CLI["cmd/cli<br/>(终端)"]
        APP["internal/app<br/>(Wails 绑定)"]
        FE["frontend/<br/>(React + Zustand)"]
    end

    subgraph Assembly["装配层"]
        BOOT["internal/bootstrap<br/>(组合根)"]
        CFG["internal/config<br/>(TOML + 环境变量)"]
    end

    subgraph Adapters["适配层"]
        OAI["provider/openai"]
        ANT["provider/anthropic"]
        FAKE["provider/fake"]
        BUILTIN["tool/builtin"]
        STORE["internal/store<br/>(SQLite)"]
        MCP["internal/mcp<br/>(预留)"]
    end

    subgraph Kernel["内核层"]
        CORE["internal/core<br/>Engine · Agent · Session"]
    end

    subgraph Contracts["契约层"]
        EVENT["internal/event<br/>事件协议 · Bus"]
        PROV["internal/provider<br/>Provider 接口 · 中立消息"]
        TOOL["internal/tool<br/>Tool 接口 · Registry"]
        PERM["internal/permission<br/>Policy · Broker"]
    end

    FE -- "window.go.app.App.*<br/>agent:event" --> APP
    MAIN --> APP
    MAIN --> BOOT
    CLI --> BOOT
    APP --> CORE
    CLI --> CORE
    BOOT --> CFG
    BOOT --> CORE
    BOOT --> Adapters
    STORE --> CORE
    OAI --> PROV
    ANT --> PROV
    FAKE --> PROV
    BUILTIN --> TOOL
    MCP --> TOOL
    CORE --> EVENT
    CORE --> PROV
    CORE --> TOOL
    CORE --> PERM
    TOOL --> PERM
```

依赖方向只能**从外向内**（图中从上往下）。几条硬性规则由测试自动检查：

| 规则 | 检查位置 |
| --- | --- |
| `internal/core` 不导入 Wails、具体厂商、store、任何第三方包 | `internal/core/arch_test.go` |
| 每个包都有 `doc.go` | `internal/archtest` |
| 仓库中没有疑似密钥 | `internal/archtest` |

### 各层职责

| 层 | 包 | 职责 |
| --- | --- | --- |
| 契约 | `event` | 统一事件协议与进程内事件总线 |
| | `provider` | 厂商中立的消息模型、`Provider` 接口、适配器注册表、配置档目录 |
| | `tool` | `Tool` 接口与注册表 |
| | `permission` | 风险等级、权限策略、"等待用户确认"的会合点 |
| 内核 | `core` | agent 循环、Delta 累加、工具执行、会话、重放、`Engine` 命令接口；预留压缩/子 agent/后台任务接口 |
| 适配 | `provider/openai`、`provider/anthropic`、`provider/fake` | 各协议 ↔ 中立 Delta |
| | `tool/builtin` | 四个内置工具 |
| | `store` | `core.SessionStore` 的 SQLite / 内存实现 |
| | `mcp`、`plugin` | 预留扩展点 |
| 装配 | `config`、`bootstrap` | 读配置；把具体实现组装成 `Engine` |
| 入口 | `cmd/cli`、`internal/app` + `main.go`、`frontend` | 两种界面 |

## 3. 两个关键接口

内核对外只有两个方向，CLI 和桌面端都只依赖它们：

```go
// 命令：core.Engine 的方法
Send(ctx, text)              // 发送消息，阻塞到本轮结束
Cancel()                     // 取消当前轮
RespondPermission(id, dec)   // 回复权限确认
NewSession() / LoadSession(id) / ListSessions() / DeleteSession(id)
SetModel(profile, model) / Status() / Profiles()

// 事件：core.Engine.Bus()
Subscribe(func(event.Event))
```

内核对内也只依赖接口：

```go
provider.Provider  // Stream(ctx, req, onDelta) error
tool.Tool          // Spec() / Summarize(input) / Execute(ctx, call)
permission.Policy  // Evaluate / Remember / Reset
core.SessionStore  // Save / Load / List / Delete
core.Compactor     // 预留：上下文压缩
```

## 4. 一次完整请求的时序

用户在桌面端输入"帮我写个文件"，模型先调用 `write_file`（需要确认），再给出回答。

```mermaid
sequenceDiagram
    autonumber
    actor U as 用户
    participant FE as 前端<br/>Composer / chatStore
    participant APP as internal/app<br/>App
    participant ENG as core.Engine
    participant AG as core.Agent
    participant P as Provider<br/>(openai/anthropic/fake)
    participant BUS as event.Bus
    participant BR as permission.Broker
    participant T as tool.Tool

    U->>FE: 输入并回车
    FE->>APP: Send(text)（Wails 绑定）
    APP-->>FE: 立即返回
    APP->>ENG: go Send(ctx, text)
    ENG->>AG: Run(ctx, session, text)
    AG->>BUS: turn_start, user_message
    BUS-->>FE: agent:event → applyEvent → 渲染

    rect rgba(124,156,255,0.08)
    note over AG,P: step 1：调用模型
    AG->>BUS: message_start
    AG->>P: Stream(ctx, req, onDelta)
    loop SSE 流
        P-->>AG: Delta(thinking / text)
        AG->>AG: accumulator.add
        AG->>BUS: thinking_delta / text_delta
        BUS-->>FE: 思考卡片、正文逐字出现
    end
    P-->>AG: Delta(tool_call 完整参数), Delta(usage), Delta(stop)
    AG->>BUS: tool_call, message_end
    end

    rect rgba(251,191,36,0.08)
    note over AG,T: 执行工具（需要确认）
    AG->>BR: Request(ctx, req, announce)
    BR->>BUS: announce → permission_request
    BUS-->>FE: 弹出权限确认
    U->>FE: 点击"允许"
    FE->>APP: RespondPermission(id, "allow")
    APP->>ENG: RespondPermission
    ENG->>BR: Resolve(id, allow)
    BR-->>AG: decision = allow
    AG->>BUS: permission_resolved
    AG->>T: Execute(ctx, call)
    T-->>AG: Result
    AG->>BUS: tool_result
    end

    note over AG,P: step 2：带着工具结果再次调用模型 → 只有文本 → 结束
    AG->>BUS: message_start … text_delta … message_end
    AG->>BUS: turn_end(completed)
    BUS-->>FE: 解锁输入框，状态栏刷新 token
    ENG->>ENG: Store.SaveSession
```

## 5. 关键设计决策

| 决策 | 选择 | 考虑过的方案 / 不选的原因 |
| --- | --- | --- |
| 桌面框架 | Wails v2.17 | v3 仍是 beta，API 变动风险大；内核与 Wails 解耦，迁移只影响 `internal/app` 和 `main.go` |
| 界面与内核的接口 | 命令 + 单通道事件流 | 直接让前端轮询状态：实时性差；每种事件一个通道：Wails 不保证跨通道顺序 |
| 厂商输出 | 厂商只产生 Delta，内核统一组装消息 | 让厂商返回完整消息：每个厂商重复写组装逻辑，且历史和界面可能不一致 |
| 中立消息模型 | 内容块（Anthropic 风格） | OpenAI 风格（tool 角色）：表达不了思考签名、多块交错 |
| 事件总线 | 同步回调 | channel 扇出：要处理慢订阅者、丢事件、关闭，复杂度高 |
| 权限等待 | Broker：先登记后通知 + channel 会合 | 把等待逻辑写在 UI 层：内核无法脱离 UI 测试 |
| 持久化接口位置 | 在 core 定义，store 实现 | 在 store 定义：内核将依赖 SQLite |
| 会话恢复 | 历史消息重放成事件 | 专用"加载历史"接口：前端需要两套渲染逻辑 |
| 前端绑定 | 手写 `bridge/wails.ts` | 用 `wails generate` 生成：没有 Wails 环境时前端无法构建；生成的类型不如手写精确 |

各模块的详细设计见 [`docs/design/`](design/)。
