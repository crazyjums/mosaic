package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/event"
	ext "github.com/crazyjums/myllmapp/internal/harness"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/provider/fake"
	"github.com/crazyjums/myllmapp/internal/store"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// 假引擎：按 Request 里的文字决定怎么表现，用来验证内核对外部引擎的“前后工作”。
type stubEngine struct {
	seen []ext.Request
	run  func(ctx context.Context, req ext.Request) ext.Result
}

func (s *stubEngine) Name() string { return "stub" }
func (s *stubEngine) Run(ctx context.Context, req ext.Request) ext.Result {
	s.seen = append(s.seen, req)
	return s.run(ctx, req)
}

func newHarnessEngine(t *testing.T, he ext.Harness, answer func(*core.Engine, event.PermissionRequest)) (*core.Engine, *event.Recorder, *store.Memory, string) {
	t.Helper()
	reg := provider.NewRegistry()
	reg.Register("fake", func(cfg provider.ProfileConfig) (provider.Provider, error) {
		return fake.NewWithScript(cfg.Name, fake.Script{}, 0), nil
	})
	reg.Register("stub", func(cfg provider.ProfileConfig) (provider.Provider, error) {
		return fake.NewWithScript(cfg.Name, fake.Script{}, 0), nil
	})
	catalog := provider.NewCatalog(reg, []provider.ProfileConfig{
		{Name: "fake", Type: "fake", Models: []string{"demo"}, DefaultModel: "demo"},
		{Name: "ext", Type: "stub", Models: []string{"default"}, DefaultModel: "default", Options: map[string]any{"command": "x"}},
	})
	ws := t.TempDir()
	mem := store.NewMemory()
	eng, err := core.NewEngine(core.EngineConfig{
		Catalog: catalog, Tools: tool.NewRegistry(), Store: mem, Policy: permission.NewModePolicy(permission.ModeDefault),
		Workspace: ws, DefaultProfile: "ext", DefaultModel: "default",
		Harnesses: map[string]ext.Harness{"stub": he},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := &event.Recorder{}
	eng.Bus().Subscribe(rec.Handle)
	eng.Bus().Subscribe(func(ev event.Event) {
		if p, ok := ev.Data.(event.PermissionRequest); ok && answer != nil {
			answer(eng, p)
		}
	})
	return eng, rec, mem, ws
}

func TestHarnessTurnLifecycle(t *testing.T) {
	he := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		id := event.NewID("msg")
		req.Emit(id, event.MessageStart{Provider: "ext", Model: req.Model, Step: 1})
		req.Emit(id, event.TextDelta{Text: "好的"})
		req.Emit(id, event.MessageEnd{StopReason: "end_turn"})
		return ext.Result{
			Reason: event.TurnCompleted, Resume: "thread-1", Usage: provider.Usage{InputTokens: 7, OutputTokens: 3},
			Messages: []provider.Message{{Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockText, Text: "好的"}}}},
		}
	}}
	eng, rec, mem, ws := newHarnessEngine(t, he, nil)

	res, err := eng.Send(context.Background(), "你好")
	if err != nil || res.Reason != event.TurnCompleted {
		t.Fatalf("结果: %+v %v", res, err)
	}
	if len(he.seen) != 1 {
		t.Fatalf("引擎应被调用一次: %d", len(he.seen))
	}
	r := he.seen[0]
	if r.Text != "你好" || r.Workspace != ws || r.Model != "default" || r.Resume != "" || r.Options["command"] != "x" || r.PermissionMode != permission.ModeDefault {
		t.Fatalf("交给引擎的请求: %+v", r)
	}

	// 事件序列：turn_start → user_message → 引擎的事件 → turn_end。
	var types []event.Type
	for _, ev := range rec.Events() {
		types = append(types, ev.Type)
		if ev.SessionID == "" || ev.AgentID != event.RootAgentID {
			t.Fatalf("事件缺少会话/agent: %+v", ev)
		}
	}
	want := []event.Type{event.TypeTurnStart, event.TypeUserMessage, event.TypeMessageStart, event.TypeTextDelta, event.TypeMessageEnd, event.TypeTurnEnd}
	if len(types) != len(want) {
		t.Fatalf("事件: %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("事件: %v", types)
		}
	}
	end := payloads[event.TurnEnd](rec)[0]
	if end.Usage.InputTokens != 7 || end.SessionUsage.OutputTokens != 3 {
		t.Fatalf("用量: %+v", end)
	}

	// 会话里保存了：用户消息、引擎的消息、恢复凭据、累计用量。
	st := eng.Status()
	saved, err := mem.LoadSession(context.Background(), st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.HarnessState != `{"resume":"thread-1","synced":2}` || len(saved.Messages) != 2 || saved.Messages[1].Text() != "好的" || saved.Usage.InputTokens != 7 {
		t.Fatalf("保存的会话: %+v", saved)
	}

	// 第二轮带上恢复凭据。
	if _, err := eng.Send(context.Background(), "继续"); err != nil {
		t.Fatal(err)
	}
	if he.seen[1].Resume != "thread-1" {
		t.Fatalf("第二轮应带恢复凭据: %q", he.seen[1].Resume)
	}
}

func TestHarnessPermissionGoesThroughBroker(t *testing.T) {
	var got permission.Decision
	he := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		d, err := req.Ask(ctx, permission.Request{CallID: "c1", Tool: "shell", Risk: permission.RiskExec, Summary: "执行命令：ls"})
		got = d
		return ext.Result{Reason: event.TurnCompleted, Err: err}
	}}
	eng, rec, _, _ := newHarnessEngine(t, he, func(e *core.Engine, p event.PermissionRequest) {
		if p.Tool != "shell" || p.CallID != "c1" || p.Summary == "" {
			t.Errorf("权限请求: %+v", p)
		}
		_ = e.RespondPermission(p.RequestID, "allow_session")
	})
	if _, err := eng.Send(context.Background(), "跑一下"); err != nil {
		t.Fatal(err)
	}
	if got != permission.DecisionAllowSession {
		t.Fatalf("引擎应收到用户的回答: %q", got)
	}
	req, res := payloads[event.PermissionRequest](rec), payloads[event.PermissionResolved](rec)
	if len(req) != 1 || len(res) != 1 || res[0].RequestID != req[0].RequestID || res[0].Decision != "allow_session" {
		t.Fatalf("权限事件: %+v %+v", req, res)
	}
}

func TestHarnessErrorAndCancel(t *testing.T) {
	boom := errors.New("引擎坏了")
	he := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		return ext.Result{Reason: event.TurnError, Err: boom}
	}}
	eng, rec, _, _ := newHarnessEngine(t, he, nil)
	res, err := eng.Send(context.Background(), "x")
	if err != nil || res.Reason != event.TurnError {
		t.Fatalf("结果: %+v %v", res, err)
	}
	errs := payloads[event.Error](rec)
	if len(errs) != 1 || errs[0].Message != "引擎坏了" || !errs[0].Fatal {
		t.Fatalf("错误事件: %+v", errs)
	}
	if end := payloads[event.TurnEnd](rec); len(end) != 1 || end[0].Reason != event.TurnError {
		t.Fatalf("turn_end: %+v", end)
	}

	// 取消：ctx 取消后引擎返回，这一轮记为 cancelled，且不再算错误。
	started := make(chan struct{})
	he2 := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		close(started)
		<-ctx.Done()
		return ext.Result{Reason: event.TurnCancelled}
	}}
	eng2, rec2, _, _ := newHarnessEngine(t, he2, nil)
	done := make(chan core.TurnResult, 1)
	go func() {
		r, _ := eng2.Send(context.Background(), "x")
		done <- r
	}()
	<-started
	eng2.Cancel()
	if r := <-done; r.Reason != event.TurnCancelled {
		t.Fatalf("取消: %+v", r)
	}
	if len(payloads[event.Error](rec2)) != 0 {
		t.Fatal("取消不应产生错误事件")
	}
}

func TestHarnessRejectsImages(t *testing.T) {
	he := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		t.Error("带图片时不应启动引擎")
		return ext.Result{}
	}}
	eng, rec, _, _ := newHarnessEngine(t, he, nil)
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	res, _ := eng.SendInput(context.Background(), core.UserInput{Text: "看图", Attachments: []core.Attachment{{Name: "a.png", MediaType: "image/png", Data: png}}})
	if res.Reason != event.TurnError {
		t.Fatalf("应报错: %+v", res)
	}
	if errs := payloads[event.Error](rec); len(errs) != 1 || errs[0].Code != "attachment_error" {
		t.Fatalf("错误: %+v", errs)
	}
}

func TestHarnessTextAttachmentsAreInlined(t *testing.T) {
	he := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		return ext.Result{Reason: event.TurnCompleted}
	}}
	eng, _, _, _ := newHarnessEngine(t, he, nil)
	_, err := eng.SendInput(context.Background(), core.UserInput{Text: "总结一下", Attachments: []core.Attachment{{Name: "notes.md", Text: "# 标题"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := he.seen[0].Text; got != "总结一下\n\n[Attached file: notes.md]\n```\n# 标题\n```" {
		t.Fatalf("文本: %q", got)
	}
}

// 在引擎之间切换时，引擎没见过的对话要作为背景补给它（评审指出：只发最新一句会让引擎在缺上下文的情况下继续）。
func TestHarnessCatchUpOnEngineSwitch(t *testing.T) {
	he := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		return ext.Result{Reason: event.TurnCompleted, Resume: "thread-1"}
	}}
	eng, _, mem, _ := newHarnessEngine(t, he, nil)
	ctx := context.Background()

	// 一个已经有对话的会话（之前在别的引擎里聊的），第一次交给 Codex 这类引擎。
	old := core.NewSession("sess_old", "ext", "default")
	old.Messages = []provider.Message{
		provider.UserText("先帮我读一下 main.go"),
		{Role: provider.RoleAssistant, Content: []provider.Block{
			{Type: provider.BlockThinking, Text: "机密的思考内容"},
			{Type: provider.BlockText, Text: "好的，我来读"},
			{Type: provider.BlockToolUse, ToolCallID: "c1", ToolName: "read_file", ToolInput: []byte(`{"path":"main.go"}`)},
		}},
		{Role: provider.RoleUser, Content: []provider.Block{{Type: provider.BlockToolResult, ToolCallID: "c1", Text: "package main"}}},
	}
	if err := mem.SaveSession(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.LoadSession(ctx, "sess_old"); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Send(ctx, "接着改一下"); err != nil {
		t.Fatal(err)
	}
	got := he.seen[0].Text
	for _, want := range []string{"先帮我读一下 main.go", "好的，我来读", "[调用了工具 read_file]", "[工具结果] package main", "接着改一下"} {
		if !strings.Contains(got, want) {
			t.Errorf("背景里缺少 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "机密的思考内容") {
		t.Error("别家的思考内容不能交给引擎")
	}
	if !strings.HasSuffix(got, "接着改一下") {
		t.Errorf("用户这一轮的话应该在最后: %q", got)
	}

	// 引擎已经看到了这些：下一轮不再补。
	if _, err := eng.Send(ctx, "再来一轮"); err != nil {
		t.Fatal(err)
	}
	if got := he.seen[1].Text; got != "再来一轮" {
		t.Fatalf("已同步的会话不该再补背景: %q", got)
	}

	// 之后会话被别的引擎续写了两条消息（状态里记着引擎只见过前 N 条）：只补这两条。
	st := eng.Status()
	cur, err := mem.LoadSession(ctx, st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	cur.Messages = append(cur.Messages, provider.UserText("中间在别处问的问题"), provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockText, Text: "中间在别处的回答"}}})
	if err := mem.SaveSession(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.LoadSession(ctx, "sess_old"); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Send(ctx, "回到这里"); err != nil {
		t.Fatal(err)
	}
	got = he.seen[2].Text
	if !strings.Contains(got, "中间在别处的回答") || strings.Contains(got, "先帮我读一下 main.go") {
		t.Fatalf("只应补没见过的部分:\n%s", got)
	}
}

func TestHarnessNewSessionHasNoCatchUp(t *testing.T) {
	he := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		return ext.Result{Reason: event.TurnCompleted, Resume: "t"}
	}}
	eng, _, _, _ := newHarnessEngine(t, he, nil)
	if _, err := eng.Send(context.Background(), "第一句"); err != nil {
		t.Fatal(err)
	}
	if got := he.seen[0].Text; got != "第一句" {
		t.Fatalf("全新的会话不该有背景: %q", got)
	}
}

// 外部引擎可以在运行期间补充：桌面壳连上之后才知道自己能提供哪些引擎。
func TestRegisterHarnessAtRuntime(t *testing.T) {
	first := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		return ext.Result{Reason: event.TurnCompleted, Resume: "a"}
	}}
	eng, _, _, _ := newHarnessEngine(t, first, nil)
	second := &stubEngine{run: func(ctx context.Context, req ext.Request) ext.Result {
		return ext.Result{Reason: event.TurnCompleted, Resume: "b"}
	}}
	eng.RegisterHarness("stub", second) // 替换
	if _, err := eng.Send(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if len(first.seen) != 0 || len(second.seen) != 1 {
		t.Fatalf("应由后注册的引擎处理: first=%d second=%d", len(first.seen), len(second.seen))
	}
	eng.UnregisterHarness("stub")
	res, _ := eng.NewSession()
	_ = res
	// 注销后这个配置档回到内置循环（桩厂商只会报错，而不是调用引擎）。
	if _, err := eng.Send(context.Background(), "y"); err == nil {
		// 内置循环里桩厂商的脚本为空，可能正常结束也可能报错；只要没有再调用已注销的引擎就行。
	}
	if len(second.seen) != 1 {
		t.Fatalf("注销后不应再调用引擎: %d", len(second.seen))
	}
}
