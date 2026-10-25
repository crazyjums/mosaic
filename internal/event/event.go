package event

import (
	"encoding/json"
	"fmt"
	"time"
)

// ProtocolVersion 是事件协议的版本号，写在每个事件的 "v" 字段里。
//
// 规则（详见 docs/event-protocol.md）：
//   - 新增事件类型、给载荷新增可选字段：版本号不变，前端必须忽略不认识的类型和字段；
//   - 删除/重命名字段、改变字段语义：版本号 +1。
//
// 这样前端可以在版本不匹配时给出明确提示，而不是静默渲染错乱。
const ProtocolVersion = 1

// Type 是事件类型。用字符串而不是整数枚举，是为了 JSON 可读、前端可直接做判别联合（discriminated union）。
type Type string

const (
	// 一轮（turn）：一次用户输入到 agent 停下来的整个过程，可能包含多次模型调用。
	TypeTurnStart   Type = "turn_start"
	TypeTurnEnd     Type = "turn_end"
	TypeUserMessage Type = "user_message"

	// 一条 assistant 消息 = 一次模型调用（一个 step）。
	TypeMessageStart  Type = "message_start"
	TypeTextDelta     Type = "text_delta"
	TypeThinkingDelta Type = "thinking_delta"
	TypeImage         Type = "image"
	TypeMessageEnd    Type = "message_end"
	// TypeRoute：模型组告诉界面"这条消息实际是哪个成员在应答"，以及前面有哪些尝试失败后被转交。
	TypeRoute Type = "route"

	// 工具调用与权限。
	TypeToolCall           Type = "tool_call"
	TypeToolResult         Type = "tool_result"
	TypePermissionRequest  Type = "permission_request"
	TypePermissionResolved Type = "permission_resolved"

	// 以下为预留能力（第一阶段只定义，不产生）：子 agent、后台任务、上下文压缩。
	TypeSubagentStart    Type = "subagent_start"
	TypeSubagentEnd      Type = "subagent_end"
	TypeTaskStart        Type = "task_start"
	TypeTaskLog          Type = "task_log"
	TypeTaskEnd          Type = "task_end"
	TypeContextCompacted Type = "context_compacted"

	TypeError Type = "error"

	// 会话元数据变化（目前是标题：由模型生成标题时，在一轮结束之后才会得到）。
	TypeSessionUpdated Type = "session_updated"
)

// RootAgentID 是主 agent 的 ID。子 agent 的事件会带上自己的 agent_id 和父 agent 的 parent_id，
// 前端据此把事件挂到一棵树上。
const RootAgentID = "main"

// Event 是统一事件的信封。
//
// 为什么用"信封 + 载荷"而不是每种事件一个扁平结构：
//   - 路由需要的字段（会话、agent、消息、序号）在所有事件里位置固定，订阅者不用解析载荷就能过滤；
//   - 载荷只放这一类事件特有的数据，结构清晰，新增类型不影响已有字段。
//
// 考虑过用 protobuf/oneof，但 Wails 前后端本来就走 JSON，引入代码生成对学习项目得不偿失。
type Event struct {
	Version int  `json:"v"`
	Type    Type `json:"type"`
	// ID 全局唯一，便于前端去重；Seq 在一个 Bus 内单调递增，便于排查乱序。
	ID  string `json:"id"`
	Seq uint64 `json:"seq"`

	SessionID string `json:"session_id"`
	// AgentID 是产生事件的 agent；ParentID 是它的父 agent（主 agent 为空）。
	// 有了这两个字段，任意事件都能被放进"agent 树"的正确节点下。
	AgentID  string `json:"agent_id"`
	ParentID string `json:"parent_id,omitempty"`
	// MessageID 关联到某条 assistant 消息（delta、tool_call 等都有），轮级事件为空。
	MessageID string `json:"message_id,omitempty"`

	Time time.Time `json:"ts"`
	Data Payload   `json:"data"`
}

// Payload 是事件载荷。每个载荷类型通过 EventType 声明自己属于哪种事件，
// 这样构造事件时不可能把类型和载荷配错（见 New）。
type Payload interface {
	EventType() Type
}

// New 用载荷构造一个事件。信封里的 ID / Seq / Time 由 Bus.Publish 统一填写，
// 调用方只需要关心"这是谁、关于哪条消息、发生了什么"。
func New(sessionID, agentID, messageID string, p Payload) Event {
	return Event{
		Version:   ProtocolVersion,
		Type:      p.EventType(),
		SessionID: sessionID,
		AgentID:   agentID,
		MessageID: messageID,
		Data:      p,
	}
}

// rawEvent 用于反序列化：先把 data 留成原始 JSON，读到 type 之后再决定解析成哪个结构。
type rawEvent struct {
	Event
	Data json.RawMessage `json:"data"`
}

// Decode 把一条 JSON 事件解析回 Event（载荷是具体类型）。
// 主要给测试和"回放 JSONL"用；运行时事件在进程内直接传结构体，不经过这里。
func Decode(b []byte) (Event, error) {
	var r rawEvent
	if err := json.Unmarshal(b, &r); err != nil {
		return Event{}, err
	}
	ev := r.Event
	decode, ok := payloadDecoders[ev.Type]
	if !ok {
		// 不认识的类型：保留信封，载荷置空。这与前端"忽略未知事件"的约定一致。
		return ev, fmt.Errorf("event: unknown type %q", ev.Type)
	}
	p, err := decode(r.Data)
	if err != nil {
		return ev, fmt.Errorf("event: decode %s data: %w", ev.Type, err)
	}
	ev.Data = p
	return ev, nil
}
