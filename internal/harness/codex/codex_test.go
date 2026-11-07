package codex

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/harness"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
)

type rec struct {
	mu     sync.Mutex
	events []recorded
	asks   []permission.Request
}

type recorded struct {
	msgID string
	p     event.Payload
}

func (r *rec) emit(id string, p event.Payload) {
	r.mu.Lock()
	r.events = append(r.events, recorded{id, p})
	r.mu.Unlock()
}

func (r *rec) types() []event.Type {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []event.Type
	for _, e := range r.events {
		out = append(out, e.p.EventType())
	}
	return out
}

func (r *rec) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, e := range r.events {
		if d, ok := e.p.(event.TextDelta); ok {
			b.WriteString(d.Text)
		}
	}
	return b.String()
}

// run 以某个场景运行一轮。answer 是用户对审批的回答（nil 表示不会被问到）。
func run(t *testing.T, scenario string, mutate func(*harness.Request), answer func(permission.Request) permission.Decision) (*rec, harness.Result) {
	t.Helper()
	t.Setenv(fakeEnv, scenario)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := &rec{}
	req := harness.Request{
		SessionID: "s1", Text: "帮我看看目录", Workspace: t.TempDir(), Model: "default",
		PermissionMode: permission.ModeDefault, Options: map[string]any{"command": exe},
		Emit: r.emit,
		Ask: func(ctx context.Context, p permission.Request) (permission.Decision, error) {
			r.mu.Lock()
			r.asks = append(r.asks, p)
			r.mu.Unlock()
			if answer == nil {
				t.Errorf("不应该被问到: %+v", p)
				return permission.DecisionDeny, nil
			}
			return answer(p), nil
		},
	}
	if mutate != nil {
		mutate(&req)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return r, New().Run(ctx, req)
}

func TestTurnTranslatesEvents(t *testing.T) {
	r, res := run(t, "basic", nil, nil)
	if res.Err != nil || res.Reason != event.TurnCompleted {
		t.Fatalf("结果: %+v", res)
	}
	if res.Resume != "t1" {
		t.Fatalf("应记下线程 ID: %q", res.Resume)
	}
	if got := r.text(); got != "你好，收到：帮我看看目录决定=accept；线程=t1" {
		t.Fatalf("文本: %q", got)
	}
	// 事件顺序：消息 1（思考 → 文本 → 命令调用与结果）→ 消息 2（文本 → 文件修改）。
	want := []event.Type{
		event.TypeMessageStart, event.TypeThinkingDelta, event.TypeTextDelta, event.TypeTextDelta, event.TypeToolCall, event.TypeToolResult, event.TypeMessageEnd,
		event.TypeMessageStart, event.TypeTextDelta, event.TypeToolCall, event.TypeToolResult, event.TypeMessageEnd,
	}
	got := r.types()
	if len(got) != len(want) {
		t.Fatalf("事件序列不对:\n got  %v\n want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个事件不对:\n got  %v\n want %v", i, got, want)
		}
	}

	// 同一条助手消息的事件共用一个 messageID，tool_result 的 messageID 为空。
	r.mu.Lock()
	var startID string
	for _, e := range r.events {
		switch p := e.p.(type) {
		case event.MessageStart:
			if startID == "" {
				startID = e.msgID
			}
		case event.TextDelta, event.ThinkingDelta:
			if e.msgID == "" {
				t.Errorf("增量必须挂在消息上: %+v", p)
			}
		case event.ToolResult:
			if e.msgID != "" {
				t.Errorf("tool_result 的 messageID 应为空: %q", e.msgID)
			}
			if p.CallID == "c1" && (p.IsError || !strings.Contains(p.Output, "file.txt")) {
				t.Errorf("命令结果: %+v", p)
			}
		case event.ToolCall:
			if e.msgID == "" {
				t.Errorf("tool_call 必须挂在消息上")
			}
		}
	}
	r.mu.Unlock()
	if startID == "" {
		t.Fatal("没有 message_start")
	}

	// 用量：输入 = 总输入 − 缓存命中。
	if res.Usage.InputTokens != 60 || res.Usage.CacheReadTokens != 40 || res.Usage.OutputTokens != 20 {
		t.Fatalf("用量: %+v", res.Usage)
	}
}

func TestHistoryMessagesArePaired(t *testing.T) {
	_, res := run(t, "basic", nil, nil)
	// 助手消息 → 工具结果消息 → 助手消息 → 工具结果消息，且每个 tool_use 都有同 ID 的 tool_result。
	uses, results := map[string]bool{}, map[string]bool{}
	var roles []provider.Role
	for _, m := range res.Messages {
		roles = append(roles, m.Role)
		for _, b := range m.Content {
			switch b.Type {
			case provider.BlockToolUse:
				uses[b.ToolCallID] = true
			case provider.BlockToolResult:
				results[b.ToolCallID] = true
			}
		}
	}
	for id := range uses {
		if !results[id] {
			t.Errorf("tool_use %s 没有对应的 tool_result", id)
		}
	}
	if len(uses) != 2 || !uses["c1"] || !uses["f1"] {
		t.Fatalf("工具调用: %v", uses)
	}
	if roles[0] != provider.RoleAssistant {
		t.Fatalf("角色顺序: %v", roles)
	}
	for i := 1; i < len(roles); i++ {
		if roles[i] == roles[i-1] {
			t.Fatalf("同一角色连续出现: %v", roles)
		}
	}
	first := res.Messages[0]
	var kinds []provider.BlockType
	for _, b := range first.Content {
		kinds = append(kinds, b.Type)
	}
	if len(kinds) != 3 || kinds[0] != provider.BlockThinking || kinds[1] != provider.BlockText || kinds[2] != provider.BlockToolUse {
		t.Fatalf("第一条助手消息的块: %v", kinds)
	}
}

func TestApprovalFlow(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		answer   permission.Decision
		wantCode string
		wantErr  bool
	}{
		{"approval", permission.DecisionAllow, "accept", false},
		{"approval", permission.DecisionAllowSession, "acceptForSession", false},
		{"approval", permission.DecisionDeny, "decline", true},
	} {
		r, res := run(t, tc.scenario, nil, func(permission.Request) permission.Decision { return tc.answer })
		if res.Err != nil || res.Reason != event.TurnCompleted {
			t.Fatalf("%v: %+v", tc.answer, res)
		}
		if len(r.asks) != 1 || r.asks[0].Tool != "shell" || r.asks[0].Risk != permission.RiskExec || r.asks[0].CallID != "c1" || !strings.Contains(r.asks[0].Summary, "ls -la") {
			t.Fatalf("审批请求: %+v", r.asks)
		}
		// fake 把收到的决定写进了第二条消息的文本里。
		if !strings.Contains(r.text(), "决定="+tc.wantCode) {
			t.Fatalf("%v: Codex 应收到 %s，文本=%q", tc.answer, tc.wantCode, r.text())
		}
		// 被拒绝的命令：tool_result 标记为错误。
		r.mu.Lock()
		for _, e := range r.events {
			if tr, ok := e.p.(event.ToolResult); ok && tr.CallID == "c1" && tr.IsError != tc.wantErr {
				t.Errorf("%v: 命令结果 is_error=%v，应为 %v", tc.answer, tr.IsError, tc.wantErr)
			}
		}
		r.mu.Unlock()
	}
}

func TestResume(t *testing.T) {
	r, res := run(t, "basic", func(q *harness.Request) { q.Resume = "old-thread" }, nil)
	if res.Err != nil || res.Resume != "old-thread" {
		t.Fatalf("应沿用保存的线程: %+v", res)
	}
	if !strings.Contains(r.text(), "线程=old-thread") {
		t.Fatalf("Codex 应收到 old-thread: %q", r.text())
	}
	// 保存的线程已经不存在：自动新开，并把新线程 ID 交还。
	_, res = run(t, "resume-missing", func(q *harness.Request) { q.Resume = "gone" }, nil)
	if res.Err != nil || res.Resume != "t1" {
		t.Fatalf("恢复失败应新开线程: %+v", res)
	}
}

func TestCancelInterruptsTurn(t *testing.T) {
	t.Setenv(fakeEnv, "interrupt")
	exe, _ := os.Executable()
	r := &rec{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan harness.Result, 1)
	go func() {
		done <- New().Run(ctx, harness.Request{
			Text: "x", Workspace: t.TempDir(), PermissionMode: permission.ModeDefault, Options: map[string]any{"command": exe},
			Emit: r.emit, Ask: func(context.Context, permission.Request) (permission.Decision, error) {
				return permission.DecisionDeny, nil
			},
		})
	}()
	for i := 0; i < 200 && !strings.Contains(r.text(), "思考中"); i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(r.text(), "思考中") {
		t.Fatal("测试前提：应先收到一段输出")
	}
	cancel()
	select {
	case res := <-done:
		if res.Reason != event.TurnCancelled {
			t.Fatalf("应是取消: %+v", res)
		}
		// 已经输出的内容进历史。
		if len(res.Messages) != 1 || res.Messages[0].Text() != "思考中" {
			t.Fatalf("历史: %+v", res.Messages)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("取消后没有及时结束")
	}
}

func TestFailureAndCrash(t *testing.T) {
	_, res := run(t, "failed", nil, nil)
	if res.Reason != event.TurnError || res.Err == nil || !strings.Contains(res.Err.Error(), "额度用完了") {
		t.Fatalf("失败: %+v", res)
	}
	_, res = run(t, "crash", nil, nil)
	if res.Reason != event.TurnError || res.Err == nil || !strings.Contains(res.Err.Error(), "boom") {
		t.Fatalf("崩溃应带上 stderr 的末尾: %+v", res)
	}
}

func TestNotLoggedIn(t *testing.T) {
	r, res := run(t, "unauth", nil, nil)
	if res.Reason != event.TurnError || res.Err == nil || !strings.Contains(res.Err.Error(), "codex login") {
		t.Fatalf("没登录应立刻提示 codex login: %+v", res)
	}
	if len(r.types()) != 0 {
		t.Fatalf("没登录时不应开始对话: %v", r.types())
	}
}

func TestMissingCommand(t *testing.T) {
	var emitted int
	res := New().Run(context.Background(), harness.Request{
		Text: "x", Workspace: t.TempDir(), Options: map[string]any{"command": "/nonexistent/codex-xyz"},
		Emit: func(string, event.Payload) { emitted++ },
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "codex login") {
		t.Fatalf("应给出安装和登录提示: %v", res.Err)
	}
}

func TestPolicyMapping(t *testing.T) {
	for _, tc := range []struct {
		mode              permission.Mode
		approval, sandbox string
	}{
		{permission.ModeReadOnly, "on-request", "read-only"},
		{permission.ModeDefault, "on-request", "workspace-write"},
		{permission.ModeAcceptEdits, "on-request", "workspace-write"},
		{permission.ModeAuto, "never", "danger-full-access"},
	} {
		a, s, _ := policy(tc.mode)
		if a != tc.approval || s != tc.sandbox {
			t.Errorf("%s: got %s/%s", tc.mode, a, s)
		}
	}
}
