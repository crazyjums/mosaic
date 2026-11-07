package remote

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/harness"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// fakeHost 模拟壳：RunHarness 里按脚本回调 Hub。
type fakeHost struct {
	hub       *Hub
	script    func(ctx context.Context, runID string, spec Spec) (Outcome, error)
	mu        sync.Mutex
	cancelled []string
	specs     []Spec
}

func (f *fakeHost) RunHarness(ctx context.Context, runID string, spec Spec) (Outcome, error) {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	f.mu.Unlock()
	return f.script(ctx, runID, spec)
}

func (f *fakeHost) CancelHarness(runID string) {
	f.mu.Lock()
	f.cancelled = append(f.cancelled, runID)
	f.mu.Unlock()
}

type recorded struct {
	msgID string
	p     event.Payload
}

func request(rec *[]recorded, ask func(permission.Request) permission.Decision) harness.Request {
	var mu sync.Mutex
	return harness.Request{
		SessionID: "s1", Profile: "我的cc", Text: "你好", Workspace: "/w", Model: "m", PermissionMode: permission.ModeDefault,
		Options: map[string]any{"command": "claude"}, Resume: "old",
		Emit: func(id string, p event.Payload) { mu.Lock(); *rec = append(*rec, recorded{id, p}); mu.Unlock() },
		Ask: func(ctx context.Context, r permission.Request) (permission.Decision, error) {
			if ask == nil {
				return permission.DecisionDeny, errors.New("不应被问到")
			}
			return ask(r), nil
		},
	}
}

func TestRunForwardsSpecAndRelaysEvents(t *testing.T) {
	host := &fakeHost{}
	hub := NewHub(host)
	host.hub = hub
	host.script = func(ctx context.Context, runID string, spec Spec) (Outcome, error) {
		if err := hub.Emit(runID, "m1", event.TypeMessageStart, json.RawMessage(`{"provider":"cc","model":"x","step":1}`)); err != nil {
			t.Error(err)
		}
		if err := hub.Emit(runID, "m1", event.TypeTextDelta, json.RawMessage(`{"text":"好"}`)); err != nil {
			t.Error(err)
		}
		dec, err := hub.Ask(ctx, runID, AskRequest{CallID: "c1", Tool: "Write", Risk: "write", Summary: "写入 a.txt", Input: json.RawMessage(`{"file_path":"a.txt"}`)})
		if err != nil || dec != "allow_session" {
			t.Errorf("审批: %q %v", dec, err)
		}
		return Outcome{Reason: "completed", Resume: "new-session", Usage: provider.Usage{InputTokens: 5, OutputTokens: 2},
			Messages: []provider.Message{{Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockText, Text: "好"}}}}}, nil
	}
	var rec []recorded
	var asked []permission.Request
	res := hub.Harness(TypeClaudeCode).Run(context.Background(), request(&rec, func(r permission.Request) permission.Decision {
		asked = append(asked, r)
		return permission.DecisionAllowSession
	}))
	if res.Err != nil || res.Reason != event.TurnCompleted || res.Resume != "new-session" || res.Usage.InputTokens != 5 || len(res.Messages) != 1 {
		t.Fatalf("结果: %+v", res)
	}
	sp := host.specs[0]
	if sp.Engine != "claudecode" || sp.Profile != "我的cc" || sp.SessionID != "s1" || sp.Text != "你好" || sp.Workspace != "/w" || sp.Model != "m" || sp.PermissionMode != "default" || sp.Resume != "old" || sp.Options["command"] != "claude" {
		t.Fatalf("交给壳的 spec: %+v", sp)
	}
	if len(rec) != 2 || rec[0].msgID != "m1" || rec[1].p.(event.TextDelta).Text != "好" {
		t.Fatalf("事件: %+v", rec)
	}
	if len(asked) != 1 || asked[0].Tool != "Write" || asked[0].Risk != permission.RiskWrite || asked[0].CallID != "c1" || asked[0].ID == "" {
		t.Fatalf("审批请求: %+v", asked)
	}
	// 轮结束后 runID 失效：迟到的回调被拒绝。
	if err := hub.Emit("x", "", event.TypeTextDelta, json.RawMessage(`{"text":"x"}`)); err == nil {
		t.Fatal("未登记的 runID 应被拒绝")
	}
}

func TestEmitRejectsEventsTheEngineMustNotForge(t *testing.T) {
	host := &fakeHost{}
	hub := NewHub(host)
	host.script = func(ctx context.Context, runID string, spec Spec) (Outcome, error) {
		for _, typ := range []event.Type{event.TypeTurnEnd, event.TypeTurnStart, event.TypeUserMessage, event.TypePermissionRequest, event.TypePermissionResolved, event.TypeSessionUpdated} {
			if err := hub.Emit(runID, "", typ, json.RawMessage(`{}`)); err == nil {
				t.Errorf("%s 不应允许由引擎发出", typ)
			}
		}
		if err := hub.Emit(runID, "", event.TypeTextDelta, json.RawMessage(`not json`)); err == nil {
			t.Error("坏的载荷应被拒绝")
		}
		return Outcome{Reason: "completed"}, nil
	}
	var rec []recorded
	hub.Harness(TypePi).Run(context.Background(), request(&rec, nil))
	if len(rec) != 0 {
		t.Fatalf("被拒绝的事件不应到达界面: %+v", rec)
	}
}

func TestResultMapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		out    Outcome
		err    error
		reason event.TurnEndReason
		hasErr bool
	}{
		{"取消", Outcome{Reason: "cancelled"}, nil, event.TurnCancelled, false},
		{"出错", Outcome{Reason: "error", Error: "额度用完了"}, nil, event.TurnError, true},
		{"出错没有原因", Outcome{Reason: "error"}, nil, event.TurnError, true},
		{"壳失败", Outcome{}, errors.New("壳崩了"), event.TurnError, true},
	} {
		host := &fakeHost{script: func(context.Context, string, Spec) (Outcome, error) { return tc.out, tc.err }}
		hub := NewHub(host)
		var rec []recorded
		res := hub.Harness(TypeClaudeCode).Run(context.Background(), request(&rec, nil))
		if res.Reason != tc.reason || (res.Err != nil) != tc.hasErr {
			t.Errorf("%s: %+v", tc.name, res)
		}
		if res.Resume != "old" {
			t.Errorf("%s: 引擎没有返回凭据时应保留原来的: %q", tc.name, res.Resume)
		}
	}
	host := &fakeHost{script: func(context.Context, string, Spec) (Outcome, error) {
		return Outcome{Reason: "error", Error: "额度用完了"}, nil
	}}
	var rec []recorded
	res := NewHub(host).Harness(TypeClaudeCode).Run(context.Background(), request(&rec, nil))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "额度用完了") {
		t.Fatalf("应带上引擎的错误原因: %v", res.Err)
	}
}

func TestCancelAsksHostToInterruptAndWaitsForResult(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	host := &fakeHost{}
	host.script = func(ctx context.Context, runID string, spec Spec) (Outcome, error) {
		close(started)
		<-release // 壳收到取消后整理结果
		return Outcome{Reason: "cancelled", Messages: []provider.Message{{Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockText, Text: "说到一半"}}}}}, nil
	}
	hub := NewHub(host)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan harness.Result, 1)
	var rec []recorded
	go func() { done <- hub.Harness(TypeClaudeCode).Run(ctx, request(&rec, nil)) }()
	<-started
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		host.mu.Lock()
		n := len(host.cancelled)
		host.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	host.mu.Lock()
	if len(host.cancelled) != 1 {
		host.mu.Unlock()
		t.Fatal("取消应通知壳")
	}
	host.mu.Unlock()
	select {
	case <-done:
		t.Fatal("取消后应等壳交还结果，而不是立刻返回")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	res := <-done
	if res.Reason != event.TurnCancelled || len(res.Messages) != 1 || res.Messages[0].Text() != "说到一半" {
		t.Fatalf("取消的结果应保留已有内容: %+v", res)
	}
}

func TestAskUnknownRunAndRiskFallback(t *testing.T) {
	hub := NewHub(&fakeHost{})
	if d, err := hub.Ask(context.Background(), "nope", AskRequest{Tool: "x"}); err == nil || d != "deny" {
		t.Fatalf("未知 runID: %q %v", d, err)
	}
	var got permission.Risk
	host := &fakeHost{}
	hub2 := NewHub(host)
	host.script = func(ctx context.Context, runID string, spec Spec) (Outcome, error) {
		_, _ = hub2.Ask(ctx, runID, AskRequest{Tool: "x", Risk: "weird"})
		return Outcome{Reason: "completed"}, nil
	}
	var rec []recorded
	hub2.Harness(TypePi).Run(context.Background(), request(&rec, func(r permission.Request) permission.Decision { got = r.Risk; return permission.DecisionDeny }))
	if got != permission.RiskExec {
		t.Fatalf("不认识的风险级别应按最高处理: %q", got)
	}
}

func TestStubProviderExplainsWhyItCannotRun(t *testing.T) {
	for _, typ := range []string{TypeClaudeCode, TypePi} {
		p, err := provider.DefaultRegistry.New(provider.ProfileConfig{Name: "x", Type: typ})
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		err = p.Stream(context.Background(), provider.Request{}, func(provider.Delta) {})
		if err == nil || !strings.Contains(err.Error(), "Electron") {
			t.Fatalf("%s: 应提示只在 Electron 版可用: %v", typ, err)
		}
	}
}

// 用户点停止：正在等用户回答的审批要立刻结束，而不是一直挂着。
func TestPendingAskIsCancelledWithTheRun(t *testing.T) {
	host := &fakeHost{}
	hub := NewHub(host)
	asking := make(chan struct{})
	askErr := make(chan error, 1)
	host.script = func(ctx context.Context, runID string, spec Spec) (Outcome, error) {
		go func() {
			_, err := hub.Ask(context.Background(), runID, AskRequest{Tool: "Bash", Risk: "exec"})
			askErr <- err
		}()
		<-asking
		time.Sleep(50 * time.Millisecond)
		return Outcome{Reason: "cancelled"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	var rec []recorded
	req := request(&rec, nil)
	req.Ask = func(ctx context.Context, r permission.Request) (permission.Decision, error) {
		close(asking)
		<-ctx.Done() // 用户一直没回答
		return permission.DecisionDeny, ctx.Err()
	}
	done := make(chan harness.Result, 1)
	go func() { done <- hub.Harness(TypeClaudeCode).Run(ctx, req) }()
	<-asking
	cancel()
	select {
	case err := <-askErr:
		if err == nil {
			t.Fatal("取消后等待中的审批应返回错误")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("取消后审批仍然挂着")
	}
	<-done
}
