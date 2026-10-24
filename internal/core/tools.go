package core

import (
	"context"
	"fmt"
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
func (a *Agent) runTools(ctx context.Context, em emitter, calls []provider.Block) []provider.Block {
	results := make([]provider.Block, 0, len(calls))
	for _, c := range calls {
		var r tool.Result
		if ctx.Err() != nil {
			r = tool.Errorf("cancelled by user before execution")
		} else {
			r = a.runTool(ctx, em, c)
		}
		results = append(results, provider.Block{
			Type:       provider.BlockToolResult,
			ToolCallID: c.ToolCallID,
			Text:       r.Output,
			IsError:    r.IsError,
		})
	}
	return results
}

// runTool 执行单个工具调用：查找 → 权限裁决 →（等待确认）→ 执行 → 发 tool_result。
func (a *Agent) runTool(ctx context.Context, em emitter, c provider.Block) (result tool.Result) {
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
		return tool.Errorf("unknown tool %q", c.ToolName)
	}
	spec := t.Spec()

	// 步骤 2：权限裁决。
	switch a.cfg.Policy.Evaluate(spec.Name, spec.Risk, c.ToolInput) {
	case permission.Deny:
		return tool.Errorf("permission denied by policy: %s tools are not allowed in the current mode", spec.Risk)
	case permission.Ask:
		if allowed, reason := a.askPermission(ctx, em, c, t); !allowed {
			return tool.Errorf("permission denied by user%s", reason)
		}
	}

	// 步骤 3：执行。工具 panic 不应拖垮整个应用，转成工具错误交给模型。
	// 注意 defer 的执行顺序是后进先出：recover 先改写 result，上面发事件的 defer 再读取它。
	start := time.Now()
	defer func() {
		duration = time.Since(start)
		if p := recover(); p != nil {
			result = tool.Errorf("tool panicked: %v", p)
		}
	}()
	return t.Execute(ctx, tool.Call{ID: c.ToolCallID, Input: c.ToolInput, Workspace: a.cfg.Workspace})
}

// askPermission 发出确认请求并阻塞等待用户回答。返回是否允许，以及拒绝时的补充说明。
//
// 等待期间 agent 循环停在这里；用户的回答经 Engine.RespondPermission → Broker.Resolve 送达。
// 用户点击"停止"会取消 ctx，等待立即结束并按拒绝处理。
func (a *Agent) askPermission(ctx context.Context, em emitter, c provider.Block, t tool.Tool) (bool, string) {
	spec := t.Spec()
	req := permission.Request{
		ID:      event.NewID("perm"),
		CallID:  c.ToolCallID,
		Tool:    spec.Name,
		Risk:    spec.Risk,
		Summary: t.Summarize(c.ToolInput),
		Input:   c.ToolInput,
	}

	// 先登记、后通知（由 Broker.Request 保证），通知就是发 permission_request 事件。
	decision, err := a.cfg.Broker.Request(ctx, req, func(r permission.Request) {
		em.emit("", event.PermissionRequest{
			RequestID: r.ID,
			CallID:    r.CallID,
			Tool:      r.Tool,
			Risk:      string(r.Risk),
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
	a.cfg.Policy.Remember(spec.Name, decision)
	return decision.Allowed(), ""
}
