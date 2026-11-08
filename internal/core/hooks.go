package core

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/crazyjums/myllmapp/internal/event"
)

// 钩子：在 agent 循环的几个固定位置运行用户批准过的外部命令（第一版只来自插件），
// 格式和 Claude Code 的 hooks 兼容。设计见 docs/design/hooks-and-subagents.md §2。
//
// 内核只定义“在哪里问、怎么用答案”，不管命令怎么跑：执行、匹配、超时、环境在 internal/hooks 里，
// 通过 AgentConfig.Hooks 注入（内核不依赖它，见 arch_test.go）。
//
// 原则：钩子只能让事情更严，不能更松。所以这里：
//   - 钩子说 allow 当作没表态，该弹的确认照样弹；deny 阻止，ask 把“放行”提成“要确认”；
//   - updatedInput 在权限判断之前生效，确认弹窗里显示的就是改过之后的参数；
//   - 私密工具的输出不交给钩子；
//   - 钩子加进对话的文字带“来自哪个钩子”的标注，并有长度上限；不支持改写工具结果。

// HookEvent 是钩子事件名（和 Claude Code 一致）。
type HookEvent string

const (
	HookPreToolUse       HookEvent = "PreToolUse"
	HookPostToolUse      HookEvent = "PostToolUse"
	HookUserPromptSubmit HookEvent = "UserPromptSubmit"
	HookStop             HookEvent = "Stop"
	// 子 agent 开始 / 做完。Start 不能阻止，只能补充上下文；Stop 的 block 表示“还没做完”，让它接着做。
	HookSubagentStart HookEvent = "SubagentStart"
	HookSubagentStop  HookEvent = "SubagentStop"
)

// HookInput 是交给钩子的上下文（internal/hooks 把它转成 Claude Code 格式的 JSON 写进标准输入）。
type HookInput struct {
	Event     HookEvent
	SessionID string
	Cwd       string
	AgentID   string
	// AgentType 是子 agent 的名字（SubagentStart / SubagentStop 用它匹配 matcher）。
	AgentType string

	ToolName  string
	ToolInput json.RawMessage
	ToolUseID string
	// ToolResponse 只在 PostToolUse 且工具不是私密工具时填写。
	ToolResponse    string
	HasToolResponse bool
	ToolIsError     bool

	Prompt               string
	LastAssistantMessage string
	// StopHookActive 为 true 表示这一轮已经因为 Stop 钩子继续过（防止钩子让 agent 永远停不下来）。
	StopHookActive bool
}

// HookOutcome 是所有匹配的钩子合起来的结论。
type HookOutcome struct {
	// Block：PreToolUse 不执行工具；UserPromptSubmit 不处理这条输入；PostToolUse 把原因告诉模型；
	// Stop 让 agent 接着做（原因作为提示交给模型）。
	Block  bool
	Reason string
	// Ask：PreToolUse 把“放行”提成“要确认”。
	Ask bool
	// UpdatedInput：PreToolUse 改过的工具参数（JSON 对象）。
	UpdatedInput json.RawMessage
	// Context 是钩子要加进对话的文字，每条已带来源标注。
	Context []string
	// StopTurn：钩子要求整轮停下（continue: false）。
	StopTurn   bool
	StopReason string
	// Runs 是每条钩子的运行记录，内核负责发成 hook_run 事件。
	Runs []event.HookRun
}

// Hooks 运行某个事件上匹配的钩子。没有匹配的钩子时返回零值，应当很快。
type Hooks interface {
	Run(ctx context.Context, in HookInput) HookOutcome
}

// maxHookContext 是钩子加进对话的文字的总上限（字符）。超出部分截掉并注明。
const maxHookContext = 2000

// runHooks 运行钩子并把运行记录发成事件。callID 不为空时，事件挂在那个工具卡片上。
func (a *Agent) runHooks(ctx context.Context, em emitter, in HookInput, callID string) HookOutcome {
	in.SessionID, in.Cwd, in.AgentID = a.cfg.SessionID, a.cfg.Workspace, a.cfg.ID
	if in.AgentType == "" {
		in.AgentType = a.cfg.AgentType
	}
	return runHooks(ctx, a.cfg.Hooks, em, in, callID)
}

// runHooks 是不依赖 Agent 的版本（task 工具在派出子 agent 之前用它跑 SubagentStart）。
func runHooks(ctx context.Context, h Hooks, em emitter, in HookInput, callID string) HookOutcome {
	if h == nil {
		return HookOutcome{}
	}
	if in.SessionID == "" {
		in.SessionID = em.sessionID
	}
	out := h.Run(ctx, in)
	for _, r := range out.Runs {
		r.Hook = string(in.Event)
		if r.CallID == "" {
			r.CallID = callID
		}
		em.emit("", r)
	}
	return out
}

// hookContextText 把钩子要加的文字拼成一段，带总标注和上限。
func hookContextText(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	s := strings.Join(parts, "\n\n")
	if r := []rune(s); len(r) > maxHookContext {
		s = string(r[:maxHookContext]) + "…（钩子输出超出上限，已截断）"
	}
	return "<hook-context>\n以下内容来自插件钩子，不是用户说的话：\n\n" + s + "\n</hook-context>"
}
