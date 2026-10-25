package anthropic

import (
	"encoding/json"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// messagesRequest 是 Messages API 请求体中本项目用到的字段。
type messagesRequest struct {
	Model        string          `json:"model"`
	MaxTokens    int             `json:"max_tokens"`
	System       string          `json:"system,omitempty"`
	Messages     []message       `json:"messages"`
	Tools        []toolDef       `json:"tools,omitempty"`
	Stream       bool            `json:"stream"`
	Thinking     *thinkingConfig `json:"thinking,omitempty"`
	OutputConfig *outputConfig   `json:"output_config,omitempty"`
	// Fallbacks 为 "default" 时，请求被安全策略拒绝后由服务端自动换用合适的模型重试（需配合 beta 头）。
	Fallbacks string `json:"fallbacks,omitempty"`
}

type thinkingConfig struct {
	Type    string `json:"type"`              // "adaptive"
	Display string `json:"display,omitempty"` // "summarized"：返回可读的思考摘要
}

type outputConfig struct {
	Effort string `json:"effort,omitempty"` // low / medium / high / xhigh / max
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

// contentBlock 覆盖请求中会出现的所有块类型，按 Type 使用不同字段。
type contentBlock struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"` // redacted_thinking

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	Source *imageSource `json:"source,omitempty"`
}

// MarshalJSON 保证 thinking 块总是带上 thinking 字段。
//
// 通用结构体上的 omitempty 会在思考文本为空（只有签名）时把字段整个省略，
// 而 API 要求 thinking 块必须有这个字段，缺了会返回 400，之后整个会话都无法继续。
func (c contentBlock) MarshalJSON() ([]byte, error) {
	type plain contentBlock // 去掉方法，避免递归
	if c.Type != "thinking" {
		return json.Marshal(plain(c))
	}
	return json.Marshal(struct {
		plain
		Thinking string `json:"thinking"` // 外层同名字段覆盖内嵌的 omitempty 版本
	}{plain(c), c.Thinking})
}

type imageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// convertMessages 把中立消息转换成 Messages API 的消息。
//
// 需要注意的边界：
//   - 空文本块会被 API 拒绝，跳过；
//   - 没有签名的思考块（来自其他厂商或被中断的调用）不能回传，跳过；
//   - assistant 消息中的图片 API 不接受，跳过；
//   - 转换后为空的消息整条跳过。
func convertMessages(msgs []provider.Message) []message {
	var out []message
	for _, m := range msgs {
		var blocks []contentBlock
		for _, b := range m.Content {
			if cb, ok := convertBlock(m.Role, b); ok {
				blocks = append(blocks, cb)
			}
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, message{Role: string(m.Role), Content: blocks})
	}
	return out
}

func convertBlock(role provider.Role, b provider.Block) (contentBlock, bool) {
	switch b.Type {
	case provider.BlockText:
		if b.Text == "" {
			return contentBlock{}, false
		}
		return contentBlock{Type: "text", Text: b.Text}, true
	case provider.BlockThinking:
		if role != provider.RoleAssistant || b.Signature == "" {
			return contentBlock{}, false
		}
		return contentBlock{Type: "thinking", Thinking: b.Text, Signature: b.Signature}, true
	case provider.BlockRedactedThinking:
		return contentBlock{Type: "redacted_thinking", Data: b.Data}, role == provider.RoleAssistant
	case provider.BlockToolUse:
		input := b.ToolInput
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return contentBlock{Type: "tool_use", ID: b.ToolCallID, Name: b.ToolName, Input: input}, true
	case provider.BlockToolResult:
		return contentBlock{Type: "tool_result", ToolUseID: b.ToolCallID, Content: b.Text, IsError: b.IsError}, true
	case provider.BlockImage:
		if role != provider.RoleUser {
			return contentBlock{}, false
		}
		return contentBlock{Type: "image", Source: &imageSource{Type: "base64", MediaType: b.MediaType, Data: b.ImageData}}, true
	}
	return contentBlock{}, false
}

func convertTools(specs []provider.ToolSpec) []toolDef {
	out := make([]toolDef, 0, len(specs))
	for _, s := range specs {
		out = append(out, toolDef{Name: s.Name, Description: s.Description, InputSchema: s.InputSchema})
	}
	return out
}
