package event

import (
	"encoding/json"
)

// 本文件定义每种事件的载荷。字段名使用 snake_case JSON，与前端 TypeScript 类型一一对应。
// 所有字段的含义和示例见 docs/event-protocol.md。

// Usage 是 token 用量。
// 事件包不依赖 provider 包，所以这里单独定义一份；core 负责从 provider.Usage 转换过来。
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// Add 累加用量，用于"本轮合计"和"会话合计"。
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + o.InputTokens,
		OutputTokens:     u.OutputTokens + o.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + o.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + o.CacheWriteTokens,
	}
}

// ---- 轮级事件 ----

// TurnStart 表示开始处理一次用户输入。前端据此锁定输入框、显示"运行中"。
type TurnStart struct {
	TurnID   string `json:"turn_id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Replay 为 true 表示这是会话恢复时重放的历史事件，前端可以跳过动画。
	Replay bool `json:"replay,omitempty"`
}

// TurnEndReason 说明一轮为什么结束。
type TurnEndReason string

const (
	TurnCompleted TurnEndReason = "completed" // 模型不再调用工具，正常结束
	TurnMaxSteps  TurnEndReason = "max_steps" // 达到最大步数，被内核强制停止
	TurnCancelled TurnEndReason = "cancelled" // 用户取消
	TurnError     TurnEndReason = "error"     // 出错（详情见之前的 error 事件）
)

type TurnEnd struct {
	TurnID string        `json:"turn_id"`
	Reason TurnEndReason `json:"reason"`
	Steps  int           `json:"steps"`
	// Usage 是本轮所有模型调用的合计；SessionUsage 是会话累计，状态栏直接显示它。
	Usage        Usage `json:"usage"`
	SessionUsage Usage `json:"session_usage"`
	Replay       bool  `json:"replay,omitempty"`
}

type UserMessage struct {
	Text string `json:"text"`
}

// ---- 消息级事件 ----

type MessageStart struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Step 是本轮中的第几次模型调用，从 1 开始。
	Step int `json:"step"`
}

type TextDelta struct {
	Text string `json:"text"`
}

// ThinkingDelta 是思考内容的增量。
// 注意 Anthropic 的思考签名（signature）不在这里：签名对界面没有意义，只需要存进历史消息里回传给模型。
type ThinkingDelta struct {
	Text string `json:"text"`
	// Redacted 为 true 表示厂商返回了加密的思考块，界面只能显示"思考内容已加密"。
	Redacted bool `json:"redacted,omitempty"`
}

// Image 是一张图片。第一阶段只支持内联 base64；URL 字段为后续对接对象存储/本地文件预留。
type Image struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data,omitempty"` // base64，不含 data: 前缀
	URL       string `json:"url,omitempty"`
	Alt       string `json:"alt,omitempty"`
}

type MessageEnd struct {
	// StopReason 统一为 end_turn / tool_use / max_tokens / refusal / cancelled / error 等。
	StopReason string `json:"stop_reason"`
	Usage      Usage  `json:"usage"`
}

// ---- 工具与权限 ----

type ToolCall struct {
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input"`
	Risk   string          `json:"risk"` // read / write / exec
}

type ToolResult struct {
	CallID     string `json:"call_id"`
	Name       string `json:"name"`
	Output     string `json:"output"`
	IsError    bool   `json:"is_error"`
	DurationMS int64  `json:"duration_ms"`
}

// PermissionRequest 请求用户确认一次工具调用。
// 前端弹窗后调用 RespondPermission(request_id, decision) 回复；在回复之前 agent 循环处于阻塞等待状态。
type PermissionRequest struct {
	RequestID string          `json:"request_id"`
	CallID    string          `json:"call_id"`
	Tool      string          `json:"tool"`
	Risk      string          `json:"risk"`
	Summary   string          `json:"summary"` // 工具生成的人类可读摘要，例如"写入 notes.md（120 字节）"
	Input     json.RawMessage `json:"input"`
}

type PermissionResolved struct {
	RequestID string `json:"request_id"`
	CallID    string `json:"call_id"`
	// Decision: allow / allow_session / deny；超时或取消时为 deny。
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

// ---- 预留：子 agent ----

type SubagentStart struct {
	AgentID  string `json:"agent_id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
	Task     string `json:"task"`
}

type SubagentEnd struct {
	AgentID  string `json:"agent_id"`
	ParentID string `json:"parent_id"`
	Status   string `json:"status"` // completed / failed / cancelled
	Summary  string `json:"summary,omitempty"`
}

// ---- 预留：后台任务 ----

type TaskStart struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"` // 例如 shell
}

type TaskLog struct {
	TaskID string `json:"task_id"`
	Stream string `json:"stream"` // stdout / stderr / info
	Text   string `json:"text"`
}

type TaskEnd struct {
	TaskID   string `json:"task_id"`
	Status   string `json:"status"` // completed / failed / cancelled
	ExitCode int    `json:"exit_code"`
}

// ---- 预留：上下文压缩 ----

type ContextCompacted struct {
	BeforeTokens    int    `json:"before_tokens"`
	AfterTokens     int    `json:"after_tokens"`
	RemovedMessages int    `json:"removed_messages"`
	Summary         string `json:"summary,omitempty"`
}

// ---- 错误 ----

// Error 是一个对用户可见的错误。Fatal 为 true 表示本轮因此终止，否则只是警告（例如某个工具失败）。
type Error struct {
	Code    string `json:"code"` // provider_error / tool_error / max_steps / internal ...
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

// SessionUpdated 表示会话的元数据变了，界面据此刷新标题和会话列表。
type SessionUpdated struct {
	Title string `json:"title"`
}

func (SessionUpdated) EventType() Type { return TypeSessionUpdated }

func (TurnStart) EventType() Type          { return TypeTurnStart }
func (TurnEnd) EventType() Type            { return TypeTurnEnd }
func (UserMessage) EventType() Type        { return TypeUserMessage }
func (MessageStart) EventType() Type       { return TypeMessageStart }
func (TextDelta) EventType() Type          { return TypeTextDelta }
func (ThinkingDelta) EventType() Type      { return TypeThinkingDelta }
func (Image) EventType() Type              { return TypeImage }
func (MessageEnd) EventType() Type         { return TypeMessageEnd }
func (ToolCall) EventType() Type           { return TypeToolCall }
func (ToolResult) EventType() Type         { return TypeToolResult }
func (PermissionRequest) EventType() Type  { return TypePermissionRequest }
func (PermissionResolved) EventType() Type { return TypePermissionResolved }
func (SubagentStart) EventType() Type      { return TypeSubagentStart }
func (SubagentEnd) EventType() Type        { return TypeSubagentEnd }
func (TaskStart) EventType() Type          { return TypeTaskStart }
func (TaskLog) EventType() Type            { return TypeTaskLog }
func (TaskEnd) EventType() Type            { return TypeTaskEnd }
func (ContextCompacted) EventType() Type   { return TypeContextCompacted }
func (Error) EventType() Type              { return TypeError }

// payloadDecoders 把事件类型映射到"如何解析它的载荷"。新增事件类型时在这里登记一行。
var payloadDecoders = map[Type]func(json.RawMessage) (Payload, error){}

func register[T Payload]() {
	var zero T
	payloadDecoders[zero.EventType()] = func(b json.RawMessage) (Payload, error) {
		var v T
		if len(b) == 0 {
			return v, nil
		}
		err := json.Unmarshal(b, &v)
		return v, err
	}
}

func init() {
	register[TurnStart]()
	register[TurnEnd]()
	register[UserMessage]()
	register[MessageStart]()
	register[TextDelta]()
	register[ThinkingDelta]()
	register[Image]()
	register[MessageEnd]()
	register[ToolCall]()
	register[ToolResult]()
	register[PermissionRequest]()
	register[PermissionResolved]()
	register[SubagentStart]()
	register[SubagentEnd]()
	register[TaskStart]()
	register[TaskLog]()
	register[TaskEnd]()
	register[ContextCompacted]()
	register[Error]()
	register[SessionUpdated]()
}

// AllTypes 返回所有已登记的事件类型，测试用它检查"每种事件都能往返序列化"。
func AllTypes() []Type {
	out := make([]Type, 0, len(payloadDecoders))
	for t := range payloadDecoders {
		out = append(out, t)
	}
	return out
}
