# 如何扩展：新增厂商 / 工具 / 子 agent

所有扩展都遵循同一个模式：**实现接口 → 注册**，不修改 `internal/core`。

## 1. 新增厂商

### 情况 A：厂商提供 OpenAI 兼容接口（大多数）

**不需要写代码**，在配置文件里加一个配置档：

```toml
[[providers]]
name = "moonshot"
type = "openai"
api_key_env = "MOONSHOT_API_KEY"
base_url = "https://api.moonshot.cn/v1"
models = ["kimi-k2"]
```

如果厂商在流式 delta 里用 `reasoning_content` 或 `reasoning` 返回思考，界面会自动显示思考卡片。

### 情况 B：一种新协议

以一个假想的 "acme" 协议为例。

**第 1 步：新建包 `internal/provider/acme/`**，至少包含 `doc.go` 和实现：

```go
// internal/provider/acme/acme.go
package acme

import (
    "context"
    "errors"

    "github.com/crazyjums/myllmapp/internal/provider"
)

func init() {
    // 适配器类型名，配置里 type = "acme" 时使用本适配器。
    provider.Register("acme", New)
}

type Provider struct {
    name, apiKey, baseURL string
}

func New(cfg provider.ProfileConfig) (provider.Provider, error) {
    if cfg.APIKey == "" {
        return nil, errors.New("missing API key")
    }
    return &Provider{name: cfg.Name, apiKey: cfg.APIKey, baseURL: cfg.BaseURL}, nil
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) error {
    // 1. 把 req.Messages（中立格式）转换成 acme 的请求体
    // 2. 发起 HTTP 请求（务必使用 ctx），用 sse.NewReader 读取流
    // 3. 把每个厂商事件翻译成 Delta：
    onDelta(provider.Delta{Type: provider.DeltaThinking, Text: "..."}) // 有思考就发，没有就不发
    onDelta(provider.Delta{Type: provider.DeltaText, Text: "..."})
    //    工具调用参数拼接完整后再发：
    //    onDelta(provider.Delta{Type: provider.DeltaToolCall, ToolCall: &provider.ToolCall{ID: id, Name: name, Input: args}})
    // 4. 结束时发用量和停止原因
    onDelta(provider.Delta{Type: provider.DeltaUsage, Usage: &provider.Usage{InputTokens: 1, OutputTokens: 1}})
    onDelta(provider.Delta{Type: provider.DeltaStop, StopReason: provider.StopEndTurn})
    return nil
}
```

**第 2 步：在 `internal/provider/all/all.go` 加一行空导入**：

```go
_ "github.com/crazyjums/myllmapp/internal/provider/acme"
```

**第 3 步：配置**：

```toml
[[providers]]
name = "acme"
type = "acme"
api_key_env = "ACME_API_KEY"
models = ["acme-large"]
```

**第 4 步：测试**。参考 `internal/provider/openai/openai_test.go`：用 `httptest.Server` 回放一段录制的流，断言 Delta 序列。

Provider 必须遵守的约定（详见 [`provider.go`](../internal/provider/provider.go) 和 [厂商适配设计](design/providers.md)）：

- 工具调用参数拼完整再发，且必须是合法 JSON（不合法时包装成 `{"_raw": "..."}`）；
- `usage` 以最后一次为准；正常结束时最后发 `stop`；
- ctx 取消时尽快返回 `ctx.Err()`。

`internal/core/arch_test.go` 会确保你没有不小心让内核依赖新包。

## 2. 新增工具

以一个 `grep` 工具为例。

**第 1 步：实现 `tool.Tool`**（内置工具放在 `internal/tool/builtin/`，一个文件一个工具）：

```go
// internal/tool/builtin/grep.go
package builtin

import (
    "bufio"
    "context"
    "encoding/json"
    "fmt"
    "os"
    "strings"

    "github.com/crazyjums/myllmapp/internal/permission"
    "github.com/crazyjums/myllmapp/internal/tool"
)

type Grep struct{}

type grepInput struct {
    Path    string `json:"path"`
    Pattern string `json:"pattern"`
}

func (t *Grep) Spec() tool.Spec {
    return tool.Spec{
        Name:        "grep",
        Description: "在工作区内的一个文件中查找包含指定字符串的行。",
        InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string"},
    "pattern": {"type": "string"}
  },
  "required": ["path", "pattern"]
}`),
        Risk: permission.RiskRead, // 只读：默认策略下无需确认
    }
}

func (t *Grep) Summarize(input json.RawMessage) string {
    var in grepInput
    _ = json.Unmarshal(input, &in)
    return fmt.Sprintf("在 %s 中查找 %q", in.Path, in.Pattern)
}

func (t *Grep) Execute(ctx context.Context, call tool.Call) tool.Result {
    var in grepInput
    if err := decode(call.Input, &in); err != nil {
        return tool.Errorf("%v", err)
    }
    path, err := resolvePath(call.Workspace, in.Path) // 必须限制在工作区内
    if err != nil {
        return tool.Errorf("%v", err)
    }
    f, err := os.Open(path)
    if err != nil {
        return tool.Errorf("open failed: %v", err)
    }
    defer f.Close()
    var out strings.Builder
    sc := bufio.NewScanner(f)
    for n := 1; sc.Scan(); n++ {
        if strings.Contains(sc.Text(), in.Pattern) {
            fmt.Fprintf(&out, "%d: %s\n", n, sc.Text())
        }
    }
    return tool.Result{Output: out.String()}
}
```

**第 2 步：注册**。内置工具在 `builtin.Register` 里加一行：

```go
reg.MustRegister(&Grep{})
```

不属于内置的工具（例如某个项目专用工具），在 `internal/bootstrap/bootstrap.go` 创建注册表之后调用 `tools.Register(...)`。

要点：

- 失败返回 `tool.Errorf(...)`，**不要 panic、不要返回 Go error**——模型需要看到失败原因；
- 声明正确的 `Risk`：读 → `RiskRead`，改文件 → `RiskWrite`，执行任意代码/网络副作用 → `RiskExec`；
- 尊重 `ctx` 取消；输出可能很大时用 `truncate` 截断。

前端不需要任何修改：工具卡片是通用的。

### MCP 工具

`internal/mcp` 已经实现了 `ToolAdapter`，把 MCP server 的工具包装成 `tool.Tool`。实现 `mcp.Client` 后调用：

```go
mcp.RegisterServerTools(ctx, tools, "github", client, permission.RiskExec)
```

工具会以 `mcp__github__<name>` 的名字出现。详见 [roadmap](roadmap.md#mcp-接入)。

## 3. 新增子 agent 定义

子 agent 的**定义与注册**已实现，**调度**是第二阶段的工作（见 [roadmap](roadmap.md#子-agent-调度与可视化树)）。现在就可以注册定义：

```go
// 在 internal/bootstrap 中，Build 创建 engine 之后：
engine.Subagents().Register(core.SubagentDefinition{
    Name:        "explorer",
    Description: "只读地探索代码库，回答'某某功能在哪里实现'这类问题。",
    System:      "You are a read-only code explorer. Never modify files.",
    Tools:       []string{"read_file", "list_dir"}, // 只给只读工具
    MaxSteps:    10,
})
```

调度实现后，主 agent 会看到一个 `task` 工具，可以把任务派给 `explorer`；子 agent 的所有事件带着自己的 `agent_id` 和 `parent_id = "main"`，前端的 `ActivityPanel` 会把它们画成树。

## 4. 新增事件类型

见 [事件协议 §7](event-protocol.md#7-修改协议的清单)。

## 5. 新增界面

任何能调用 `core.Engine` 并订阅 `Engine.Bus()` 的程序都是一个界面。`cmd/cli` 是最小的参考实现（约 300 行）：

1. `config.Load` → `bootstrap.Build` 拿到 `Engine`；
2. `Engine.Bus().Subscribe(...)` 渲染事件；
3. 收到 `permission_request` 时调用 `Engine.RespondPermission`；
4. 调用 `Engine.Send` 发送消息。

例如做一个 Web 版：在第 2 步把事件写进 WebSocket / SSE，在 HTTP handler 里调用命令即可。
