package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// runTools 依次执行一条 assistant 消息里的所有工具调用，返回对应的 tool_result 块。
//
// 不变式：返回的结果数量 == calls 数量，且一一对应。即使中途被取消，剩下的调用也会得到
// "cancelled" 结果——厂商协议要求每个 tool_use 都有配对的 tool_result。
//
// 取舍：顺序执行 vs. 并行执行。并行更快，但多个权限弹窗同时出现会让用户困惑，
// 对第一阶段的四个工具来说也没有明显收益。以后可以对 read 风险的工具并行执行。
//
// stop 不为空表示有钩子要求整轮停下（continue: false）：剩下的调用不再执行，同样补上结果。
func (a *Agent) runTools(ctx context.Context, em emitter, calls []provider.Block) (results []provider.Block, stop string) {
	results = make([]provider.Block, 0, len(calls))
	for _, c := range calls {
		var r tool.Result
		if stop != "" {
			r = tool.Errorf("not executed: a hook stopped this turn")
			em.emit("", event.ToolResult{CallID: c.ToolCallID, Name: c.ToolName, Output: r.Output, IsError: true})
		} else if ctx.Err() != nil {
			r = tool.Errorf("cancelled by user before execution")
			// 即使没有执行，也必须发终态事件，避免前端工具卡片一直停在 queued。
			em.emit("", event.ToolResult{
				CallID: c.ToolCallID, Name: c.ToolName,
				Output: r.Output, IsError: true,
			})
		} else {
			r, stop = a.runTool(ctx, em, c)
		}
		results = append(results, provider.Block{
			Type:       provider.BlockToolResult,
			ToolCallID: c.ToolCallID,
			Text:       r.Output,
			IsError:    r.IsError,
		})
	}
	return results, stop
}

// runTool 执行单个工具调用：查找 → PreToolUse 钩子 → 权限裁决 →（等待确认）→ 执行 → PostToolUse 钩子 → 发 tool_result。
// stop 不为空表示钩子要求整轮停下。
func (a *Agent) runTool(ctx context.Context, em emitter, c provider.Block) (result tool.Result, stop string) {
	// 耗时只统计工具真正执行的时间，不包括等待用户确认的时间；没有执行（被拒绝、未知工具）则为 0。
	var duration time.Duration
	// 无论从哪个分支返回，都发一个 tool_result 事件，界面上的工具卡片才能进入"完成"状态。
	defer func() {
		em.emit("", event.ToolResult{
			CallID:     c.ToolCallID,
			Name:       c.ToolName,
			Output:     result.Output,
			IsError:    result.IsError,
			DurationMS: duration.Milliseconds(),
		})
	}()

	// 步骤 1：查找工具。模型有时会"编造"不存在的工具，告诉它出错即可，它通常会自我纠正。
	t, ok := a.cfg.Tools.Get(c.ToolName)
	if !ok {
		return tool.Errorf("unknown tool %q", c.ToolName), ""
	}
	// 被禁用的工具不会出现在发给模型的工具列表里，但历史消息里可能还有它的调用示例，模型偶尔会照着调用。
	if a.cfg.Disabled[c.ToolName] {
		return tool.Errorf("tool %q has been disabled by the user", c.ToolName), ""
	}
	spec := t.Spec()

	// 步骤 2：PreToolUse 钩子。放在权限之前：它改过的参数要经过权限判断，确认弹窗里显示的就是改后的参数。
	var hookCtx []string
	hookAsk := false
	if a.cfg.Hooks != nil {
		ho := a.runHooks(ctx, em, HookInput{Event: HookPreToolUse, ToolName: spec.Name, ToolInput: c.ToolInput, ToolUseID: c.ToolCallID}, c.ToolCallID)
		if ho.StopTurn {
			return tool.Errorf("blocked by hook: %s", orDefault(ho.StopReason, "a hook stopped this turn")), orDefault(ho.StopReason, "插件钩子停止了这一轮")
		}
		if ho.Block {
			return tool.Errorf("blocked by hook: %s", orDefault(ho.Reason, "no reason given")), ""
		}
		if len(ho.UpdatedInput) > 0 {
			c.ToolInput = ho.UpdatedInput // c 是副本：历史里模型原来的调用不变，执行和确认用改后的参数
		}
		hookAsk, hookCtx = ho.Ask, ho.Context
	}

	// 步骤 3：权限裁决。
	verdict := a.cfg.Policy.Evaluate(spec.Name, spec.Risk, c.ToolInput)
	// 私密工具永远要问：档位放行、规则放行、本会话已允许都不算数。拒绝仍然是拒绝。
	// 钩子说 ask 也一样：只能把放行提成确认，不能把拒绝降成确认。
	if (spec.Sensitive || hookAsk) && verdict == permission.Allow {
		verdict = permission.Ask
	}
	switch verdict {
	case permission.Deny:
		return tool.Errorf("permission denied by policy: %s tools are not allowed in the current mode", spec.Risk), ""
	case permission.Ask:
		if allowed, reason := a.askPermission(ctx, em, c, t, hookAsk); !allowed {
			return tool.Errorf("permission denied by user%s", reason), ""
		}
	}

	// 步骤 4：执行。耗时只算工具本身，不含钩子。
	start := time.Now()
	result = a.execute(ctx, t, c)
	duration = time.Since(start)

	// 步骤 5：PostToolUse 钩子。私密工具的输出不交给钩子：一个插件的钩子不该读到另一个插件拿到的私密内容。
	if a.cfg.Hooks != nil && ctx.Err() == nil {
		in := HookInput{Event: HookPostToolUse, ToolName: spec.Name, ToolInput: c.ToolInput, ToolUseID: c.ToolCallID, ToolIsError: result.IsError}
		if !spec.Sensitive {
			in.ToolResponse, in.HasToolResponse = result.Output, true
		}
		ho := a.runHooks(ctx, em, in, c.ToolCallID)
		if ho.Block && ho.Reason != "" {
			hookCtx = append(hookCtx, "PostToolUse 钩子的反馈："+ho.Reason)
		}
		hookCtx = append(hookCtx, ho.Context...)
		if ho.StopTurn {
			stop = orDefault(ho.StopReason, "插件钩子停止了这一轮")
		}
	}
	if s := hookContextText(hookCtx); s != "" {
		result.Output += "\n\n" + s
	}
	return result, stop
}

// execute 调用工具。工具 panic 不应拖垮整个应用，转成工具错误交给模型。
func (a *Agent) execute(ctx context.Context, t tool.Tool, c provider.Block) (result tool.Result) {
	defer func() {
		if p := recover(); p != nil {
			result = tool.Errorf("tool panicked: %v", p)
		}
	}()
	outside := a.cfg.AllowOutsideWorkspace != nil && a.cfg.AllowOutsideWorkspace()
	return t.Execute(ctx, tool.Call{ID: c.ToolCallID, Input: c.ToolInput, Workspace: a.cfg.Workspace, AllowOutsideWorkspace: outside, SessionID: a.cfg.SessionID})
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// askPermission 发出确认请求并阻塞等待用户回答。返回是否允许，以及拒绝时的补充说明。
//
// 等待期间 agent 循环停在这里；用户的回答经 Engine.RespondPermission → Broker.Resolve 送达。
// 用户点击"停止"会取消 ctx，等待立即结束并按拒绝处理。
//
// byHook 为 true 表示这次确认是钩子要求的（ask）：用户的回答只算这一次，不记成“本会话总是允许”，
// 否则钩子想每次都问的调用会在第一次之后被悄悄放行。
func (a *Agent) askPermission(ctx context.Context, em emitter, c provider.Block, t tool.Tool, byHook bool) (bool, string) {
	spec := t.Spec()
	req := permission.Request{
		ID:        event.NewID("perm"),
		CallID:    c.ToolCallID,
		Tool:      spec.Name,
		Risk:      spec.Risk,
		Sensitive: spec.Sensitive,
		Summary:   t.Summarize(c.ToolInput),
		Input:     c.ToolInput,
	}

	// 先登记、后通知（由 Broker.Request 保证），通知就是发 permission_request 事件。
	decision, err := a.cfg.Broker.Request(ctx, req, func(r permission.Request) {
		em.emit("", event.PermissionRequest{
			RequestID: r.ID,
			CallID:    r.CallID,
			Tool:      r.Tool,
			Risk:      string(r.Risk),
			Sensitive: r.Sensitive,
			Summary:   r.Summary,
			Input:     r.Input,
		})
	})

	resolved := event.PermissionResolved{RequestID: req.ID, CallID: req.CallID, Decision: string(decision)}
	if err != nil {
		resolved.Decision = string(permission.DecisionDeny)
		resolved.Reason = "cancelled"
	}
	em.emit("", resolved)

	if err != nil {
		return false, fmt.Sprintf(" (%v)", err)
	}
	if !spec.Sensitive && !byHook {
		a.cfg.Policy.Remember(spec.Name, decision)
	}
	return decision.Allowed(), ""
}
