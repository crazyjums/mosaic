package chatgpt

import (
	"encoding/json"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// 以下是 Responses API 的请求结构，只包含本项目用到的字段。

type request struct {
	Model        string          `json:"model"`
	Instructions string          `json:"instructions,omitempty"`
	Input        []inputItem     `json:"input"`
	Tools        []functionTool  `json:"tools,omitempty"`
	Reasoning    *reasoningParam `json:"reasoning,omitempty"`
	// 套餐额度要求：不在服务端保存对话（每次带上完整历史），必须流式。
	Store  bool `json:"store"`
	Stream bool `json:"stream"`
}

type reasoningParam struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"` // auto：返回思考摘要，界面显示在思考卡片里
}

// inputItem 是一个输入条目。用一个扁平结构覆盖三种条目（message / function_call / function_call_output），
// 由 Type 区分——与 provider.Block 的取舍相同。
type inputItem struct {
	Type string `json:"type"`
	// message
	Role    string        `json:"role,omitempty"`
	Content []contentPart `json:"content,omitempty"`
	// function_call / function_call_output
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // 与 Chat Completions 一样，参数是 JSON 字符串
	Output    string `json:"output,omitempty"`
}

type contentPart struct {
	Type     string `json:"type"` // input_text / input_image / output_text
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"` // data:<media>;base64,<data>
}

type functionTool struct {
	Type        string          `json:"type"` // function
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

// convertInput 把中立消息转换成 Responses API 的输入条目。
//
// 主要差异：
//   - 一条中立消息可能拆成多个条目：assistant 的文本是一个 message 条目，每个工具调用是一个 function_call 条目；
//     user 消息里的工具结果是 function_call_output 条目，其余内容是一个 message 条目；
//   - 用户输入用 input_text / input_image，模型的回答用 output_text；
//   - 思考块不回传：store=false 时要回传思考需要请求加密的思考内容，套餐这条路未必支持，没有它模型照样能继续。
func convertInput(msgs []provider.Message) []inputItem {
	var out []inputItem
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			var parts []contentPart
			for _, b := range m.Content {
				switch b.Type {
				case provider.BlockToolResult:
					output := b.Text
					if b.IsError && output == "" {
						output = "error"
					}
					out = append(out, inputItem{Type: "function_call_output", CallID: b.ToolCallID, Output: output})
				case provider.BlockText:
					parts = append(parts, contentPart{Type: "input_text", Text: b.Text})
				case provider.BlockImage:
					parts = append(parts, contentPart{Type: "input_image", ImageURL: "data:" + b.MediaType + ";base64," + b.ImageData})
				}
			}
			if len(parts) > 0 {
				out = append(out, inputItem{Type: "message", Role: "user", Content: parts})
			}
		case provider.RoleAssistant:
			var parts []contentPart
			var calls []inputItem
			for _, b := range m.Content {
				switch b.Type {
				case provider.BlockText:
					if b.Text != "" {
						parts = append(parts, contentPart{Type: "output_text", Text: b.Text})
					}
				case provider.BlockToolUse:
					args := string(b.ToolInput)
					if args == "" {
						args = "{}"
					}
					calls = append(calls, inputItem{Type: "function_call", CallID: b.ToolCallID, Name: b.ToolName, Arguments: args})
				}
			}
			// 文本在前、调用在后，与模型输出的顺序一致。
			if len(parts) > 0 {
				out = append(out, inputItem{Type: "message", Role: "assistant", Content: parts})
			}
			out = append(out, calls...)
		}
	}
	return out
}

func convertTools(specs []provider.ToolSpec) []functionTool {
	out := make([]functionTool, 0, len(specs))
	for _, s := range specs {
		params := s.InputSchema
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, functionTool{Type: "function", Name: s.Name, Description: s.Description, Parameters: params})
	}
	return out
}
