package harness

import (
	"context"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// Harness 是一个外部 agent 引擎。
type Harness interface {
	// Name 是引擎类型，和厂商配置档的 type 一致（例如 "codex"）。
	Name() string
	// Run 处理一轮用户输入，返回时这一轮已经结束（正常、出错或被取消）。
	// 过程中通过 Request.Emit 发事件、通过 Request.Ask 请求用户确认。
	Run(ctx context.Context, req Request) Result
}

// Request 是一轮的输入。
type Request struct {
	SessionID string
	// Text 是用户这一轮说的话；Attachments 里的文本附件已经并入 Text 之后（见 core），图片目前不支持。
	Text string
	// Workspace 是会话的工作区（绝对路径），引擎在这个目录里工作。
	Workspace string
	// Model 是配置档里选中的模型；为空或 "default" 表示让引擎用自己的默认模型。
	Model string
	// PermissionMode 是 Mosaic 当前的权限模式（readonly / default / acceptEdits / auto），引擎据此选择自己的沙箱和审批策略。
	PermissionMode permission.Mode
	// Options 是配置档里引擎特有的选项（例如可执行文件路径）。
	Options map[string]any
	// Resume 是上一轮返回的恢复凭据（引擎私有的不透明字符串，例如 Codex 的线程 ID）；第一轮为空。
	Resume string

	// Emit 发一个事件，事件会带上会话 ID 和根 agent ID。messageID 把 message_start / text_delta / thinking_delta /
	// tool_call / message_end 归到同一条助手消息（用 event.NewID("msg") 生成）；轮级事件、tool_result、error 传空串。
	// 事件的顺序约定与内置循环一致（见 docs/event-protocol.md）。
	Emit func(messageID string, p event.Payload)
	// Ask 请求用户确认一个操作，阻塞到用户回答或 ctx 取消。它负责发 permission_request / permission_resolved 事件。
	// 返回 deny 加上 error 表示被取消。
	Ask func(ctx context.Context, r permission.Request) (permission.Decision, error)
}

// Result 是一轮的结果。
type Result struct {
	Reason event.TurnEndReason
	Err    error
	// Resume 是下一轮要带上的恢复凭据；引擎没有变化时原样返回 Request.Resume。
	Resume string
	// Usage 是这一轮的 token 用量（引擎不报告时为零）。
	Usage provider.Usage
	// Messages 是这一轮要追加进会话历史的消息（用户消息之后的部分：助手文本、工具调用、工具结果）。
	// 结构与内置循环产生的一致，所以会话重放、导出和切换到内置引擎继续聊都能用。
	Messages []provider.Message
}
