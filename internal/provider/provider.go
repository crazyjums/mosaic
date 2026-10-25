package provider

import (
	"context"
	"encoding/json"
)

// ToolSpec 是发给模型的工具定义。它是 tool.Spec 的"模型视角"子集，
// 由 core 从工具注册表转换而来，这样 provider 包不需要依赖 tool 包。
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Request 是一次模型调用的输入，与厂商无关。
type Request struct {
	Model    string
	System   string
	Messages []Message
	Tools    []ToolSpec
	// MaxTokens 为 0 时由适配器使用自己的默认值。
	MaxTokens int
}

// DeltaType 是流式增量的类型。
type DeltaType string

const (
	DeltaText              DeltaType = "text"               // 文本片段
	DeltaThinking          DeltaType = "thinking"           // 思考片段
	DeltaThinkingSignature DeltaType = "thinking_signature" // 当前思考块的签名（Anthropic）
	DeltaRedactedThinking  DeltaType = "redacted_thinking"  // 一个完整的加密思考块
	DeltaToolCall          DeltaType = "tool_call"          // 一个"完整的"工具调用（参数已拼接好）
	DeltaImage             DeltaType = "image"              // 一张完整的图片
	DeltaUsage             DeltaType = "usage"              // 本次调用的用量；可出现多次，以最后一次为准（值为累计值）
	DeltaStop              DeltaType = "stop"               // 本次调用结束，带停止原因
	DeltaRoute             DeltaType = "route"              // 模型组告诉上层"实际是哪个成员在应答"（含被转交的尝试）；在第一个内容之前发出
)

// Delta 是厂商流式输出被翻译后的最小单位。
//
// 为什么只有这么少的类型：厂商协议千差万别（SSE 事件名、字段名、分片方式都不同），
// 但对 agent 循环而言，有用的信息只有"文本、思考、工具调用、图片、用量、停止原因"这几类。
// 把差异封装在适配器里，内核和前端就只需要理解这一小套词汇。
type Delta struct {
	Type DeltaType

	// Text：text / thinking 的文本片段；thinking_signature 的签名；redacted_thinking 的加密数据。
	Text       string
	ToolCall   *ToolCall
	Image      *Image
	Usage      *Usage
	StopReason StopReason
	// Route 只用于 DeltaRoute。
	Route *Route
}

// Route 描述一次调用最终由谁应答，以及前面有哪些尝试失败后被转交。
type Route struct {
	Group   string `json:"group"`
	Profile string `json:"profile"`
	Model   string `json:"model"`
	// Try 是第几次尝试（1 表示第一个成员就成功了）。
	Try      int       `json:"try"`
	HandedOn []Attempt `json:"handed_on,omitempty"`
}

// Attempt 是一次失败后被转交的尝试。
type Attempt struct {
	Profile string    `json:"profile"`
	Model   string    `json:"model"`
	Kind    ErrorKind `json:"kind"`
	Message string    `json:"message"`
}

// ToolCall 是一个完整的工具调用。
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Image 是模型输出的一张图片。
type Image struct {
	MediaType string
	Data      string // base64
}

// StopReason 是统一后的停止原因。
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"   // 模型自然结束
	StopToolUse   StopReason = "tool_use"   // 模型请求调用工具
	StopMaxTokens StopReason = "max_tokens" // 输出被 max_tokens 截断
	StopRefusal   StopReason = "refusal"    // 被厂商安全策略拒绝
	StopOther     StopReason = "other"
)

// Provider 是所有模型厂商适配器都要实现的接口。
//
// 为什么用"回调"而不是返回 channel 或 iterator：
//   - channel 需要额外的 goroutine 和关闭约定，错误还得另开一个 channel 或包在元素里；
//   - Go 1.23 的 iter.Seq2 很优雅，但对刚接触的人不够直观；
//   - 回调 + 返回 error 是最朴素的写法：Stream 返回时调用一定结束了，错误就是返回值。
//
// 为什么 Stream 不返回组装好的完整消息：组装逻辑由 core 统一实现（core/accumulator.go），
// 保证"存进历史的消息"和"界面看到的事件"出自同一个 Delta 序列，厂商实现也因此更短。
//
// 约定：
//   - onDelta 在调用 Stream 的 goroutine 中被同步调用；
//   - 工具调用参数必须拼接完整后再以一个 DeltaToolCall 发出；
//   - 正常结束时最后发一个 DeltaStop；
//   - ctx 取消时应尽快返回 ctx.Err()。
type Provider interface {
	// Name 返回配置档名称（例如 "deepseek"），用于界面显示和会话记录。
	Name() string
	Stream(ctx context.Context, req Request, onDelta func(Delta)) error
}

// ModelLister 是可选能力：能从厂商服务器获取可用模型列表的适配器实现它。
//
// 用"可选接口 + 类型断言"而不是把 ListModels 加进 Provider：
// 不是每个厂商都提供模型列表接口（fake 没有，一些兼容实现也没有），
// 强制所有适配器实现只会多出一堆返回"不支持"的空方法。调用方这样使用：
//
//	if l, ok := p.(provider.ModelLister); ok { models, err := l.ListModels(ctx) }
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}
