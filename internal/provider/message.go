package provider

import "encoding/json"

// Role 是消息角色。中立模型只有两种角色：工具结果作为 user 消息里的 tool_result 块存在。
//
// 为什么不像 OpenAI 那样单独有 "tool" 角色：Anthropic 的"内容块"模型是两者的超集——
// 一条消息可以同时包含思考、文本、多个工具调用；把它降级成 OpenAI 格式很容易（拆分即可），
// 反过来则会丢信息（比如思考块的签名）。所以中立模型选更丰富的那一种。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType 是内容块类型。
type BlockType string

const (
	BlockText             BlockType = "text"
	BlockThinking         BlockType = "thinking"
	BlockRedactedThinking BlockType = "redacted_thinking" // Anthropic 加密思考块，只能原样回传
	BlockToolUse          BlockType = "tool_use"
	BlockToolResult       BlockType = "tool_result"
	BlockImage            BlockType = "image"
)

// Block 是一条消息中的一个内容块。
//
// 设计取舍：用一个"扁平结构 + Type 判别"而不是每种块一个 Go 类型加接口。
//   - 优点：JSON 序列化/持久化/前端 TS 类型都非常直接，不需要自定义 MarshalJSON；
//   - 缺点：某些字段只对某些类型有意义（用注释标明），需要靠 Type 判断。
//
// 对一个学习项目而言，"打开数据库就能看懂存的是什么"比类型系统上的严谨更重要。
type Block struct {
	Type BlockType `json:"type"`

	// text / thinking：文本内容。tool_result 也用 Text 存工具输出。
	Text string `json:"text,omitempty"`
	// thinking：Anthropic 的思考签名，回传历史时必须原样带上，否则 API 会拒绝。
	Signature string `json:"signature,omitempty"`
	// redacted_thinking：加密的思考数据。
	Data string `json:"data,omitempty"`

	// tool_use / tool_result：工具调用 ID，用来把结果和调用配对。
	ToolCallID string `json:"tool_call_id,omitempty"`
	// tool_use：工具名和参数（JSON 对象）。
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	// tool_result：工具是否执行失败。
	IsError bool `json:"is_error,omitempty"`

	// image：媒体类型和 base64 数据。
	MediaType string `json:"media_type,omitempty"`
	ImageData string `json:"image_data,omitempty"`

	// Attachment 非空表示这个 text / image 块来自用户添加的附件，值是原文件名。
	// 发给模型时和普通块一样处理；区别只在于界面和标题：附件显示成"附件条"，而不是用户打的字。
	Attachment string `json:"attachment,omitempty"`
}

// Message 是一条对话消息。
type Message struct {
	Role    Role    `json:"role"`
	Content []Block `json:"content"`
}

// Text 拼接消息中所有文本块（不含附件），常用于标题生成、日志、测试断言。
// 附件的文字内容不算"用户说的话"：它可能是一整个文件，不应该出现在标题和用户气泡里。
func (m Message) Text() string {
	var s string
	for _, b := range m.Content {
		if b.Type == BlockText && b.Attachment == "" {
			s += b.Text
		}
	}
	return s
}

// HasAttachment 报告消息里有没有附件块。
func (m Message) HasAttachment() bool {
	for _, b := range m.Content {
		if b.Attachment != "" {
			return true
		}
	}
	return false
}

// IsUserInput 判断这是不是用户的一次发言（开启新的一轮）：user 消息里有文字或附件。
// 只含 tool_result 的 user 消息是工具结果，不算。
func (m Message) IsUserInput() bool {
	return m.Role == RoleUser && (m.Text() != "" || m.HasAttachment())
}

// ToolCalls 返回消息中的所有工具调用块。
func (m Message) ToolCalls() []Block {
	var out []Block
	for _, b := range m.Content {
		if b.Type == BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}

// UserText 构造一条纯文本 user 消息。
func UserText(text string) Message {
	return Message{Role: RoleUser, Content: []Block{{Type: BlockText, Text: text}}}
}

// Usage 是一次调用的 token 用量。各厂商字段名不同，适配器负责换算到这里。
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + o.InputTokens,
		OutputTokens:     u.OutputTokens + o.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + o.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + o.CacheWriteTokens,
	}
}
