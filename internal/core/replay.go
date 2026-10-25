package core

import (
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// ReplayEvents 把一个会话的历史消息转换成事件序列。
//
// 为什么要"重放"而不是给前端一个"加载历史"的专用接口：前端只有一条渲染路径（消费事件），
// 恢复会话时也走这条路，就不会出现"实时渲染和历史渲染长得不一样"的问题。
//
// 代价：历史消息里没有的信息无法重放（工具耗时、权限确认过程、每条消息的用量），
// 这些在恢复后的界面上不显示。第一阶段可以接受；以后如需完整回放，可以把事件本身也持久化。
//
// riskOf 用于给重放的 tool_call 补上风险等级（历史消息里不存这个信息），可以为 nil。
func ReplayEvents(s *Session, riskOf func(toolName string) permission.Risk) []event.Event {
	snap := s.Snapshot()
	var out []event.Event
	add := func(msgID string, p event.Payload) {
		out = append(out, event.New(snap.ID, event.RootAgentID, msgID, p))
	}

	toolNames := map[string]string{} // call_id → 工具名，tool_result 块里没有工具名
	turnOpen := false
	var turnID string
	closeTurn := func() {
		if turnOpen {
			add("", event.TurnEnd{TurnID: turnID, Reason: event.TurnCompleted, Replay: true, SessionUsage: toEventUsage(snap.Usage)})
			turnOpen = false
		}
	}

	step := 0
	for _, m := range snap.Messages {
		switch m.Role {
		case provider.RoleUser:
			// 含文本的 user 消息是"用户发言"，开启新的一轮；只含 tool_result 的是工具结果。
			if text := m.Text(); text != "" {
				closeTurn()
				turnID = event.NewID("turn")
				turnOpen = true
				step = 0
				add("", event.TurnStart{TurnID: turnID, Provider: snap.Provider, Model: snap.Model, Replay: true})
				add("", event.UserMessage{Text: text})
			}
			for _, b := range m.Content {
				if b.Type == provider.BlockToolResult {
					add("", event.ToolResult{CallID: b.ToolCallID, Name: toolNames[b.ToolCallID], Output: b.Text, IsError: b.IsError})
				}
			}

		case provider.RoleAssistant:
			step++
			msgID := event.NewID("msg")
			add(msgID, event.MessageStart{Provider: snap.Provider, Model: snap.Model, Step: step})
			stop := string(provider.StopEndTurn)
			for _, b := range m.Content {
				switch b.Type {
				case provider.BlockText:
					add(msgID, event.TextDelta{Text: b.Text})
				case provider.BlockThinking:
					if b.Text != "" {
						add(msgID, event.ThinkingDelta{Text: b.Text})
					}
				case provider.BlockRedactedThinking:
					add(msgID, event.ThinkingDelta{Redacted: true})
				case provider.BlockToolUse:
					toolNames[b.ToolCallID] = b.ToolName
					stop = string(provider.StopToolUse)
					tc := event.ToolCall{CallID: b.ToolCallID, Name: b.ToolName, Input: b.ToolInput}
					if riskOf != nil {
						tc.Risk = string(riskOf(b.ToolName))
					}
					add(msgID, tc)
				case provider.BlockImage:
					add(msgID, event.Image{MediaType: b.MediaType, Data: b.ImageData})
				}
			}
			add(msgID, event.MessageEnd{StopReason: stop})
		}
	}
	closeTurn()
	return out
}
