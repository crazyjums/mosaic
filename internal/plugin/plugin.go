package plugin

import (
	"context"
	"encoding/json"
)

// HookPoint 是钩子的触发时机。
type HookPoint string

const (
	UserPromptSubmit HookPoint = "user_prompt_submit"
	PreToolUse       HookPoint = "pre_tool_use"
	PostToolUse      HookPoint = "post_tool_use"
	TurnEnd          HookPoint = "turn_end"
)

// HookInput 是传给钩子的上下文。
type HookInput struct {
	Point     HookPoint
	SessionID string
	ToolName  string
	ToolInput json.RawMessage
	Output    string // PostToolUse：工具输出
	Prompt    string // UserPromptSubmit：用户输入
}

// HookDecision 是钩子的处理结果。
type HookDecision struct {
	// Block 为 true 时阻止后续动作（不发送 / 不执行工具），Reason 会反馈给用户或模型。
	Block  bool
	Reason string
	// Rewrite 非空时替换原内容（提示词或工具输出）。
	Rewrite string
}

type Hook interface {
	Point() HookPoint
	Handle(ctx context.Context, in HookInput) (HookDecision, error)
}

// StatusInfo 是状态栏脚本可以读取的信息。CommandStatusLine 把它序列化成 JSON 写到脚本的标准输入。
type StatusInfo struct {
	SessionID    string `json:"session_id"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Workspace    string `json:"workspace"`
	// Extra 是界面层算出的附加信息（上下文占用、花费、git 分支等），字段由调用方决定。
	Extra map[string]any `json:"extra,omitempty"`
}

// StatusLine 生成自定义状态栏文本。
type StatusLine interface {
	Render(ctx context.Context, info StatusInfo) (string, error)
}
