package openai

import (
	"encoding/json"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// 以下是 Chat Completions 的请求结构，只包含本项目用到的字段。

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	Tools         []chatTool     `json:"tools,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	// 不同厂商对输出上限字段名不统一：老字段 max_tokens 兼容面最广；
	// OpenAI 新模型要求 max_completion_tokens。由配置项 max_tokens_field 选择，见 openai.go。
	MaxTokens           int `json:"max_tokens,omitempty"`
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"` // string 或 []contentPart
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolCallFunc `json:"function"`
}

type chatToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // 注意：OpenAI 协议里参数是 JSON 字符串，而不是 JSON 对象
}

// convertMessages 把中立消息转换成 Chat Completions 的消息列表。
//
// 主要差异：
//   - 系统提示词是一条 role=system 的消息；
//   - 工具结果不是 user 消息里的块，而是独立的 role=tool 消息，每个结果一条；
//   - assistant 的工具调用放在 tool_calls 字段，参数是字符串；
//   - 思考块没有对应字段，丢弃。
func convertMessages(system string, msgs []provider.Message) []chatMessage {
	var out []chatMessage
	if system != "" {
		out = append(out, chatMessage{Role: "system", Content: system})
	}
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			out = append(out, convertUser(m)...)
		case provider.RoleAssistant:
			if cm, ok := convertAssistant(m); ok {
				out = append(out, cm)
			}
		}
	}
	return out
}

func convertUser(m provider.Message) []chatMessage {
	var out []chatMessage
	var parts []contentPart
	hasImage := false
	for _, b := range m.Content {
		switch b.Type {
		case provider.BlockToolResult:
			// tool 消息必须紧跟在对应的 assistant 消息之后，所以先输出。
			content := b.Text
			if content == "" {
				content = "(empty)"
			}
			out = append(out, chatMessage{Role: "tool", ToolCallID: b.ToolCallID, Content: content})
		case provider.BlockText:
			parts = append(parts, contentPart{Type: "text", Text: b.Text})
		case provider.BlockImage:
			hasImage = true
			parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{
				URL: "data:" + b.MediaType + ";base64," + b.ImageData,
			}})
		}
	}
	if len(parts) == 0 {
		return out
	}
	// 纯文本时用字符串形式：部分兼容厂商不支持 content 数组。
	if !hasImage {
		text := ""
		for _, p := range parts {
			text += p.Text
		}
		return append(out, chatMessage{Role: "user", Content: text})
	}
	return append(out, chatMessage{Role: "user", Content: parts})
}

func convertAssistant(m provider.Message) (chatMessage, bool) {
	cm := chatMessage{Role: "assistant"}
	text := ""
	for _, b := range m.Content {
		switch b.Type {
		case provider.BlockText:
			text += b.Text
		case provider.BlockToolUse:
			args := string(b.ToolInput)
			if args == "" {
				args = "{}"
			}
			cm.ToolCalls = append(cm.ToolCalls, chatToolCall{
				ID: b.ToolCallID, Type: "function",
				Function: chatToolCallFunc{Name: b.ToolName, Arguments: args},
			})
		}
	}
	if text != "" {
		cm.Content = text
	}
	// 只有思考块的 assistant 消息（例如被中断时）在 OpenAI 协议里无法表达，跳过。
	if text == "" && len(cm.ToolCalls) == 0 {
		return cm, false
	}
	return cm, true
}

func convertTools(specs []provider.ToolSpec) []chatTool {
	out := make([]chatTool, 0, len(specs))
	for _, s := range specs {
		out = append(out, chatTool{Type: "function", Function: chatFunction{
			Name: s.Name, Description: s.Description, Parameters: s.InputSchema,
		}})
	}
	return out
}
