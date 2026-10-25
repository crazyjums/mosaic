package core

import (
	"context"
	"fmt"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// DefaultMaxSteps 是一轮中最多调用模型的次数。
// 步数限制是 agent 循环的"保险丝"：模型可能陷入反复调用工具的死循环，每一步都在花钱。
const DefaultMaxSteps = 20

// AgentConfig 是一个 agent 运行所需的全部依赖。
//
// 所有依赖都以接口或值注入，Agent 自己不创建任何东西——这让同一个 Agent 实现
// 既能作为主 agent 运行，也能在将来以不同的工具集/系统提示词作为子 agent 运行（只要换一份配置）。
type AgentConfig struct {
	// ID / ParentID 写入每个事件的信封，用于在界面上组成 agent 树。主 agent 用 event.RootAgentID。
	ID       string
	ParentID string

	Provider  provider.Provider
	Model     string
	System    string
	MaxTokens int
	MaxSteps  int

	Tools     *tool.Registry
	Policy    permission.Policy
	Broker    *permission.Broker
	Publisher event.Publisher
	Compactor Compactor

	// Workspace 是工具的工作区根目录（绝对路径）。
	Workspace string
	// SessionID 传给工具（todo_write 等按会话保存状态的工具需要它）。
	SessionID string
	// Disabled 是被用户在设置里关掉的工具：不发给模型，模型硬要调用也会被拒绝。
	Disabled map[string]bool
}

// Agent 执行 agent 循环。它是无状态的（状态都在 Session 里），可以重复使用。
type Agent struct {
	cfg AgentConfig
}

func NewAgent(cfg AgentConfig) *Agent {
	if cfg.ID == "" {
		cfg.ID = event.RootAgentID
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	if cfg.Compactor == nil {
		cfg.Compactor = NoopCompactor{}
	}
	if cfg.Tools == nil {
		cfg.Tools = tool.NewRegistry()
	}
	if cfg.Policy == nil {
		cfg.Policy = permission.NewModePolicy(permission.ModeDefault)
	}
	if cfg.Broker == nil {
		cfg.Broker = permission.NewBroker()
	}
	return &Agent{cfg: cfg}
}

// TurnResult 是一轮的结果。事件流里已经有全部细节，这里只给调用方（Engine、测试）一个摘要。
type TurnResult struct {
	TurnID string
	Reason event.TurnEndReason
	Steps  int
	Usage  provider.Usage
	Err    error
}

// emitter 绑定了会话和 agent 身份的发布器，省得每次发事件都写一长串参数。
type emitter struct {
	pub       event.Publisher
	sessionID string
	agentID   string
	parentID  string
}

func (e emitter) emit(messageID string, p event.Payload) {
	if e.pub == nil {
		return
	}
	ev := event.New(e.sessionID, e.agentID, messageID, p)
	ev.ParentID = e.parentID
	e.pub.Publish(ev)
}

// Run 处理一次用户输入，是整个 harness 最核心的函数。
//
// 流程：
//
//	turn_start → user_message
//	循环 step = 1..MaxSteps：
//	  ① 上下文准备（压缩钩子）
//	  ② 调用模型，流式翻译 Delta → 事件，同时累加成 assistant 消息
//	  ③ 没有工具调用 → 结束；有 → ④
//	  ④ 逐个执行工具（可能需要等待用户确认），结果作为 user 消息追加，回到 ①
//	turn_end
//
// Run 会修改 sess（追加消息、累计用量）。调用方需保证同一会话同一时间只有一个 Run。
func (a *Agent) Run(ctx context.Context, sess *Session, input string) TurnResult {
	em := emitter{pub: a.cfg.Publisher, sessionID: sess.ID, agentID: a.cfg.ID, parentID: a.cfg.ParentID}
	res := TurnResult{TurnID: event.NewID("turn")}

	// end 统一负责收尾：发 turn_end 并返回结果。所有退出路径都经过这里，保证前端一定能解锁输入框。
	end := func(reason event.TurnEndReason, err error) TurnResult {
		res.Reason, res.Err = reason, err
		total := sess.Snapshot().Usage
		em.emit("", event.TurnEnd{
			TurnID:       res.TurnID,
			Reason:       reason,
			Steps:        res.Steps,
			Usage:        toEventUsage(res.Usage),
			SessionUsage: toEventUsage(total),
		})
		return res
	}

	em.emit("", event.TurnStart{TurnID: res.TurnID, Provider: a.cfg.Provider.Name(), Model: a.cfg.Model})
	sess.appendMessage(provider.UserText(input))
	em.emit("", event.UserMessage{Text: input})

	for step := 1; ; step++ {
		// 保险丝：超过最大步数直接停止。此时最后一条消息是工具结果，下次用户发言时模型能看到完整上下文。
		if step > a.cfg.MaxSteps {
			em.emit("", event.Error{
				Code:    "max_steps",
				Message: fmt.Sprintf("已达到最大步数 %d，agent 循环被停止。可以发送“继续”让它接着做。", a.cfg.MaxSteps),
				Fatal:   true,
			})
			return end(event.TurnMaxSteps, nil)
		}
		res.Steps = step

		// ① 上下文准备：压缩钩子。第一阶段是 no-op，接口和事件已经接好，见 compact.go。
		msgs, err := a.prepareContext(ctx, sess, em)
		if err != nil {
			em.emit("", event.Error{Code: "context_error", Message: err.Error(), Fatal: true})
			return end(event.TurnError, err)
		}

		// ② 调用模型。
		msg, stop, usage, err := a.callModel(ctx, em, step, msgs)
		res.Usage = res.Usage.Add(usage)
		sess.addUsage(usage)
		if err != nil {
			// 中途失败：把已经输出的文本存进历史（界面上用户已经看到了），丢掉不完整的部分。
			if partial := salvagePartial(msg); len(partial.Content) > 0 {
				sess.appendMessage(partial)
			}
			if ctx.Err() != nil {
				return end(event.TurnCancelled, ctx.Err())
			}
			em.emit("", event.Error{Code: "provider_error", Message: err.Error(), Fatal: true})
			return end(event.TurnError, err)
		}
		sess.appendMessage(msg)

		// ③ 没有工具调用：模型认为任务完成了。
		calls := msg.ToolCalls()
		if len(calls) == 0 {
			if stop == provider.StopMaxTokens {
				em.emit("", event.Error{Code: "max_tokens", Message: "模型输出达到 max_tokens 上限被截断。", Fatal: false})
			}
			return end(event.TurnCompleted, nil)
		}

		// ④ 执行工具。即使中途取消，runTools 也会为每个调用补上结果，保证历史合法。
		results := a.runTools(ctx, em, calls)
		sess.appendMessage(provider.Message{Role: provider.RoleUser, Content: results})
		if ctx.Err() != nil {
			return end(event.TurnCancelled, ctx.Err())
		}
	}
}

// prepareContext 取出要发给模型的消息，并在需要时调用压缩器。
func (a *Agent) prepareContext(ctx context.Context, sess *Session, em emitter) ([]provider.Message, error) {
	msgs := sess.messages()
	r, err := a.cfg.Compactor.Compact(ctx, CompactRequest{Messages: msgs, Model: a.cfg.Model})
	if err != nil {
		return nil, err
	}
	if !r.Changed {
		return msgs, nil
	}
	// 压缩会改写历史：新的消息列表替换会话里的旧列表，并通知界面。
	sess.replaceMessages(r.Messages)
	em.emit("", event.ContextCompacted{
		BeforeTokens:    r.BeforeTokens,
		AfterTokens:     r.AfterTokens,
		RemovedMessages: len(msgs) - len(r.Messages),
		Summary:         r.Summary,
	})
	return r.Messages, nil
}

// callModel 完成一次模型调用：message_start → 若干 delta 事件 → message_end。
//
// 这里是"厂商输出 → 统一事件"的转换点：provider 只产生 Delta，
// 每个 Delta 同时做两件事——喂给累加器（形成历史消息），翻译成事件（推给界面）。
func (a *Agent) callModel(ctx context.Context, em emitter, step int, msgs []provider.Message) (provider.Message, provider.StopReason, provider.Usage, error) {
	msgID := event.NewID("msg")
	em.emit(msgID, event.MessageStart{Provider: a.cfg.Provider.Name(), Model: a.cfg.Model, Step: step})

	req := provider.Request{
		Model:     a.cfg.Model,
		System:    a.cfg.System,
		Messages:  msgs,
		Tools:     a.toolSpecs(),
		MaxTokens: a.cfg.MaxTokens,
	}

	var acc accumulator
	err := a.cfg.Provider.Stream(ctx, req, func(d provider.Delta) {
		acc.add(d)
		a.translateDelta(em, msgID, d)
	})

	stop := string(acc.stop)
	if err != nil {
		stop = "error"
		if ctx.Err() != nil {
			stop = "cancelled"
		}
	}
	em.emit(msgID, event.MessageEnd{StopReason: stop, Usage: toEventUsage(acc.usage)})
	return acc.message(), acc.stop, acc.usage, err
}

// translateDelta 把一个 Delta 翻译成统一事件。没有对应事件的 Delta（签名、用量、停止）只进累加器。
func (a *Agent) translateDelta(em emitter, msgID string, d provider.Delta) {
	switch d.Type {
	case provider.DeltaText:
		em.emit(msgID, event.TextDelta{Text: d.Text})
	case provider.DeltaThinking:
		em.emit(msgID, event.ThinkingDelta{Text: d.Text})
	case provider.DeltaRedactedThinking:
		em.emit(msgID, event.ThinkingDelta{Redacted: true})
	case provider.DeltaToolCall:
		if d.ToolCall == nil {
			return
		}
		em.emit(msgID, event.ToolCall{
			CallID: d.ToolCall.ID,
			Name:   d.ToolCall.Name,
			Input:  d.ToolCall.Input,
			Risk:   string(a.riskOf(d.ToolCall.Name)),
		})
	case provider.DeltaImage:
		if d.Image == nil {
			return
		}
		em.emit(msgID, event.Image{MediaType: d.Image.MediaType, Data: d.Image.Data})
	}
}

func (a *Agent) toolSpecs() []provider.ToolSpec {
	specs := a.cfg.Tools.Specs()
	out := make([]provider.ToolSpec, 0, len(specs))
	for _, s := range specs {
		if a.cfg.Disabled[s.Name] {
			continue
		}
		out = append(out, provider.ToolSpec{Name: s.Name, Description: s.Description, InputSchema: s.InputSchema})
	}
	return out
}

func (a *Agent) riskOf(name string) permission.Risk {
	if t, ok := a.cfg.Tools.Get(name); ok {
		return t.Spec().Risk
	}
	return ""
}

func toEventUsage(u provider.Usage) event.Usage {
	return event.Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
	}
}
