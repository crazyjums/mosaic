package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/harness"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// 外部 agent 引擎（Codex 等，见 internal/harness）驱动的一轮。
//
// 和 Agent.RunInput 的分工：RunInput 自己循环调用模型、执行工具；这里把整轮交给引擎，
// 内核只负责“前后”——发 turn_start / user_message，把引擎产生的事件原样发布，把引擎的审批请求接到权限确认，
// 把结果消息存进会话历史，最后发 turn_end。界面、历史、权限弹窗因此和内置引擎完全一样。

// harnessInput 把用户消息拼成给引擎的纯文本：用户说的话，加上文本附件（带文件名）。引擎目前不接图片。
func harnessInput(m provider.Message) (string, error) {
	var b strings.Builder
	for _, blk := range m.Content {
		switch {
		case blk.Type == provider.BlockImage:
			return "", fmt.Errorf("%s：这个引擎暂不支持图片附件", blk.Attachment)
		case blk.Type == provider.BlockText && blk.Attachment == "":
			b.WriteString(blk.Text)
		}
	}
	for _, blk := range m.Content {
		if blk.Type == provider.BlockText && blk.Attachment != "" {
			// 附件块的文字已经带了“[Attached file: 名字]”和代码围栏（见 attachment.go），原样接在后面。
			b.WriteString("\n\n" + blk.Text)
		}
	}
	return b.String(), nil
}

// runHarness 用引擎 h 运行一轮。调用方已经占用了会话的运行权（runs），并负责保存会话。
func (e *Engine) runHarness(ctx context.Context, sess *Session, h harness.Harness, pcfg provider.ProfileConfig, model string, rt RuntimeConfig, in UserInput, pub event.Publisher) TurnResult {
	em := emitter{pub: pub, sessionID: sess.ID, agentID: event.RootAgentID}
	res := TurnResult{TurnID: event.NewID("turn")}
	end := func(reason event.TurnEndReason, err error) TurnResult {
		res.Reason, res.Err = reason, err
		em.emit("", event.TurnEnd{
			TurnID:       res.TurnID,
			Reason:       reason,
			Steps:        res.Steps,
			Usage:        toEventUsage(res.Usage),
			SessionUsage: toEventUsage(sess.Snapshot().Usage),
		})
		return res
	}

	userMsg, err := buildUserMessage(in)
	if err == nil {
		var text string
		if text, err = harnessInput(userMsg); err == nil {
			return e.driveHarness(ctx, sess, h, pcfg, model, rt, in, userMsg, text, em, &res, end)
		}
	}
	em.emit("", event.Error{Code: "attachment_error", Message: err.Error(), Fatal: true})
	return end(event.TurnError, err)
}

func (e *Engine) driveHarness(ctx context.Context, sess *Session, h harness.Harness, pcfg provider.ProfileConfig, model string, rt RuntimeConfig, in UserInput, userMsg provider.Message, text string, em emitter, res *TurnResult, end func(event.TurnEndReason, error) TurnResult) TurnResult {
	em.emit("", event.TurnStart{TurnID: res.TurnID, Provider: pcfg.Name, Model: model})
	sess.appendMessage(userMsg)
	em.emit("", event.UserMessage{Text: in.Text, Attachments: attachmentInfos(userMsg)})

	req := harness.Request{
		SessionID:      sess.ID,
		Text:           text,
		Workspace:      rt.Workspace,
		Model:          model,
		PermissionMode: permission.Mode(e.permissionMode()),
		Options:        pcfg.Options,
		Resume:         sess.harnessState(),
		Emit:           func(messageID string, p event.Payload) { em.emit(messageID, p) },
		Ask: func(ctx context.Context, r permission.Request) (permission.Decision, error) {
			return e.askHarness(ctx, em, r)
		},
	}
	out := h.Run(ctx, req)

	// 不管怎么结束，已经发生的内容都进历史：用户已经在界面上看到了。
	for _, m := range out.Messages {
		sess.appendMessage(m)
	}
	sess.addUsage(out.Usage)
	sess.setHarnessState(out.Resume)
	res.Usage = out.Usage
	res.Steps = 1

	reason := out.Reason
	if reason == "" {
		reason = event.TurnCompleted
	}
	if ctx.Err() != nil && reason == event.TurnCompleted {
		reason = event.TurnCancelled
	}
	if out.Err != nil && reason != event.TurnCancelled {
		em.emit("", event.Error{Code: "harness_error", Message: out.Err.Error(), Fatal: true})
		reason = event.TurnError
	}
	return end(reason, out.Err)
}

// askHarness 把引擎的审批请求变成 Mosaic 的权限确认：发 permission_request，等用户回答，发 permission_resolved。
// 与 Agent.askPermission 的区别是这里不记“本会话总是允许”：引擎有自己的会话级授权缓存（acceptForSession）。
func (e *Engine) askHarness(ctx context.Context, em emitter, r permission.Request) (permission.Decision, error) {
	if r.ID == "" {
		r.ID = event.NewID("perm")
	}
	decision, err := e.broker.Request(ctx, r, func(r permission.Request) {
		em.emit("", event.PermissionRequest{
			RequestID: r.ID, CallID: r.CallID, Tool: r.Tool, Risk: string(r.Risk),
			Sensitive: r.Sensitive, Summary: r.Summary, Input: r.Input,
		})
	})
	resolved := event.PermissionResolved{RequestID: r.ID, CallID: r.CallID, Decision: string(decision)}
	if err != nil {
		resolved.Decision = string(permission.DecisionDeny)
		resolved.Reason = "cancelled"
	}
	em.emit("", resolved)
	return decision, err
}
