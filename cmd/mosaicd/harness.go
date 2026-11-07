package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/harness/remote"
	"github.com/crazyjums/myllmapp/internal/rpc"
)

// 远程引擎（Claude Code、pi）的壳侧接口。官方 SDK 跑在 Electron 的 utilityProcess 里，见 internal/harness/remote。
//
// 壳 → Go（只有壳的主进程能调用；渲染进程的白名单是握手时报告的绑定方法，里面没有 harness.*）：
//
//	harness.register {engines:[…]}                              壳能提供哪些引擎，Go 据此注册
//	harness.emit     {run_id, message_id, type, data}           引擎产生了一个事件
//	harness.ask      {run_id, call_id, tool, risk, summary, …}  引擎要用户确认，阻塞到回答，返回 "allow" / "allow_session" / "deny"
//
// Go → 壳：
//
//	host.harness.run    {run_id, spec}   运行一轮，阻塞到结束，返回 remote.Outcome
//	host.harness.cancel {run_id}         通知：中断这一轮

type harnessHost struct{ peer *rpc.Peer }

func (h *harnessHost) RunHarness(ctx context.Context, runID string, spec remote.Spec) (remote.Outcome, error) {
	var out remote.Outcome
	err := h.peer.Call(ctx, "host.harness.run", map[string]any{"run_id": runID, "spec": spec}, &out)
	return out, err
}

func (h *harnessHost) CancelHarness(runID string) {
	_ = h.peer.Notify("host.harness.cancel", map[string]string{"run_id": runID})
}

// 壳可以注册的引擎。写死白名单：壳报告什么就注册什么的话，一个有问题的壳就能把任意厂商类型的会话劫持给自己。
var remoteEngines = map[string]bool{remote.TypeClaudeCode: true, remote.TypePi: true}

// harnessCall 处理壳发来的 harness.* 调用。
func harnessCall(ctx context.Context, eng *core.Engine, hub *remote.Hub, method string, params json.RawMessage) (any, error) {
	invalid := func(err error) (any, error) {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()}
	}
	switch method {
	case "harness.register":
		var p struct {
			Engines []string `json:"engines"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return invalid(err)
		}
		var registered []string
		for _, name := range p.Engines {
			if !remoteEngines[name] {
				continue // 不认识的引擎名忽略（壳比 Go 新时）
			}
			eng.RegisterHarness(name, hub.Harness(name))
			registered = append(registered, name)
		}
		return map[string]any{"registered": registered}, nil

	case "harness.emit":
		var p struct {
			RunID     string          `json:"run_id"`
			MessageID string          `json:"message_id"`
			Type      event.Type      `json:"type"`
			Data      json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return invalid(err)
		}
		if err := hub.Emit(p.RunID, p.MessageID, p.Type, p.Data); err != nil {
			return nil, err
		}
		return nil, nil

	case "harness.ask":
		var p struct {
			RunID string `json:"run_id"`
			remote.AskRequest
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return invalid(err)
		}
		decision, err := hub.Ask(ctx, p.RunID, p.AskRequest)
		if err != nil {
			return decision, nil // 被取消：回 deny，不当作协议错误
		}
		return decision, nil
	}
	return nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: fmt.Sprintf("没有方法 %s", method)}
}

func isHarnessMethod(method string) bool { return strings.HasPrefix(method, "harness.") }
