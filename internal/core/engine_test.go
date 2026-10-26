package core_test

// 本文件用 fake provider 驱动完整的 agent 循环（外部测试包 core_test，
// 因此 internal/core 的非测试代码依然不依赖任何具体厂商）。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/provider/fake"
	"github.com/crazyjums/myllmapp/internal/store"
	"github.com/crazyjums/myllmapp/internal/tool"
	"github.com/crazyjums/myllmapp/internal/tool/builtin"
)

type harness struct {
	engine    *core.Engine
	rec       *event.Recorder
	store     *store.Memory
	workspace string
}

// newHarness 组装一个真实的 Engine：fake provider + 内置工具 + 内存存储 + 临时工作区。
// answer 决定如何回答权限请求（在事件回调里同步回答，模拟一个"秒回"的界面）。
func newHarness(t *testing.T, script fake.Script, maxSteps int, answer func(*core.Engine, event.PermissionRequest)) *harness {
	t.Helper()
	return newHarnessWith(t, script, maxSteps, answer)
}

// newHarnessWith 和 newHarness 一样，只是可以额外注册工具。
func newHarnessWith(t *testing.T, script fake.Script, maxSteps int, answer func(*core.Engine, event.PermissionRequest), extra ...tool.Tool) *harness {
	t.Helper()
	reg := provider.NewRegistry()
	reg.Register("fake", func(cfg provider.ProfileConfig) (provider.Provider, error) {
		return fake.NewWithScript(cfg.Name, script, 0), nil
	})
	reg.Register("fake-alt", func(cfg provider.ProfileConfig) (provider.Provider, error) {
		return fake.NewWithScript(cfg.Name, fake.Script{}, 0), nil
	})
	catalog := provider.NewCatalog(reg, []provider.ProfileConfig{
		{Name: "fake", Type: "fake", Models: []string{"demo"}, DefaultModel: "demo"},
		{Name: "alt", Type: "fake-alt", Models: []string{"m1", "m2"}},
	})

	tools := tool.NewRegistry()
	builtin.Register(tools, builtin.Options{ShellTimeout: 5 * time.Second})
	for _, x := range extra {
		if err := tools.Register(x); err != nil {
			t.Fatal(err)
		}
	}

	ws := t.TempDir()
	mem := store.NewMemory()
	eng, err := core.NewEngine(core.EngineConfig{
		Catalog:   catalog,
		Tools:     tools,
		Store:     mem,
		Policy:    permission.NewModePolicy(permission.ModeDefault),
		Workspace: ws,
		MaxSteps:  maxSteps,
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
	return &harness{engine: eng, rec: rec, store: mem, workspace: ws}
}

func allow(e *core.Engine, p event.PermissionRequest) { _ = e.RespondPermission(p.RequestID, "allow") }
func deny(e *core.Engine, p event.PermissionRequest)  { _ = e.RespondPermission(p.RequestID, "deny") }

// compact 去掉连续重复的类型（多个 delta 合并成一个），便于断言流程顺序。
func compact(types []event.Type) []event.Type {
	return slices.Compact(slices.Clone(types))
}

func payloads[T event.Payload](rec *event.Recorder) []T {
	var out []T
	for _, ev := range rec.Events() {
		if p, ok := ev.Data.(T); ok {
			out = append(out, p)
		}
	}
	return out
}

// 验收标准 2 的自动化版本：思考 → 工具调用 → 权限确认 → 结果 → 回答。
func TestDemoFullLoop(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, allow)
	res, err := h.engine.Send(context.Background(), "帮我做个演示")
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != event.TurnCompleted || res.Steps != 4 {
		t.Fatalf("应在第 4 步正常结束: %+v", res)
	}

	want := []event.Type{
		event.TypeTurnStart, event.TypeUserMessage,
		// step 1：思考 → 文本 → 只读工具（无需确认）
		event.TypeMessageStart, event.TypeThinkingDelta, event.TypeTextDelta, event.TypeToolCall, event.TypeMessageEnd,
		event.TypeToolResult,
		// step 2：写文件，需要确认
		event.TypeMessageStart, event.TypeThinkingDelta, event.TypeTextDelta, event.TypeToolCall, event.TypeMessageEnd,
		event.TypePermissionRequest, event.TypePermissionResolved, event.TypeToolResult,
		// step 3：执行命令，需要确认
		event.TypeMessageStart, event.TypeThinkingDelta, event.TypeToolCall, event.TypeMessageEnd,
		event.TypePermissionRequest, event.TypePermissionResolved, event.TypeToolResult,
		// step 4：最终回答 + 图片
		event.TypeMessageStart, event.TypeThinkingDelta, event.TypeTextDelta, event.TypeImage, event.TypeMessageEnd,
		event.TypeTurnEnd,
	}
	if got := compact(h.rec.Types()); !slices.Equal(got, want) {
		t.Fatalf("事件顺序不符\n got  %v\n want %v", got, want)
	}

	// 工具真的执行了：文件写入了工作区，shell 读到了内容。
	b, err := os.ReadFile(filepath.Join(h.workspace, "agent-demo", "hello.md"))
	if err != nil {
		t.Fatalf("文件应已写入: %v", err)
	}
	if !strings.Contains(string(b), "帮我做个演示") {
		t.Fatalf("文件内容应包含用户输入: %q", b)
	}
	results := payloads[event.ToolResult](h.rec)
	if len(results) != 3 || results[2].IsError || !strings.Contains(results[2].Output, "Hello from fake provider") {
		t.Fatalf("工具结果不符: %+v", results)
	}

	// 所有事件都带协议版本号和会话 ID。
	for _, ev := range h.rec.Events() {
		if ev.Version != event.ProtocolVersion || ev.SessionID == "" || ev.AgentID != event.RootAgentID {
			t.Fatalf("信封字段缺失: %+v", ev)
		}
	}

	// 每轮结束后会话被保存：1 条用户消息 + 4 条 assistant + 3 条工具结果。
	st := h.engine.Status()
	saved, err := h.store.LoadSession(context.Background(), st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 8 {
		t.Fatalf("应保存 8 条消息，实际 %d", len(saved.Messages))
	}
	if saved.Usage.OutputTokens == 0 || st.Usage != saved.Usage {
		t.Fatalf("用量应被累计并保存: status=%+v saved=%+v", st.Usage, saved.Usage)
	}
	end := payloads[event.TurnEnd](h.rec)[0]
	if end.SessionUsage.OutputTokens != saved.Usage.OutputTokens {
		t.Fatalf("turn_end 的 session_usage 应等于会话累计用量")
	}
}

func TestPermissionDeniedContinuesLoop(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, deny)
	res, _ := h.engine.Send(context.Background(), "hi")
	if res.Reason != event.TurnCompleted {
		t.Fatalf("拒绝权限后，循环应继续直到模型结束: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(h.workspace, "agent-demo", "hello.md")); !os.IsNotExist(err) {
		t.Fatal("拒绝后文件不应被写入")
	}
	results := payloads[event.ToolResult](h.rec)
	if !results[1].IsError || !strings.Contains(results[1].Output, "denied") {
		t.Fatalf("被拒绝的工具应返回错误结果: %+v", results[1])
	}
	for _, r := range payloads[event.PermissionResolved](h.rec) {
		if r.Decision != "deny" {
			t.Fatalf("决定应为 deny: %+v", r)
		}
	}
}

func TestAllowSessionRemembered(t *testing.T) {
	asked := 0
	h := newHarness(t, fake.DemoScript(), 0, func(e *core.Engine, p event.PermissionRequest) {
		asked++
		_ = e.RespondPermission(p.RequestID, "allow_session")
	})
	h.engine.Send(context.Background(), "第一轮")
	h.engine.Send(context.Background(), "第二轮")
	// 第一轮问 write_file 和 shell 各一次；第二轮都已"本会话允许"，不再询问。
	if asked != 2 {
		t.Fatalf("应只询问 2 次，实际 %d 次", asked)
	}
}

// 工具耗时只统计执行时间，不包括等待用户确认的时间。
func TestToolDurationExcludesPermissionWait(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, func(e *core.Engine, p event.PermissionRequest) {
		go func() {
			time.Sleep(150 * time.Millisecond) // 模拟用户犹豫了一下才点"允许"
			_ = e.RespondPermission(p.RequestID, "allow")
		}()
	})
	h.engine.Send(context.Background(), "hi")
	for _, r := range payloads[event.ToolResult](h.rec) {
		if r.Name == "write_file" && r.DurationMS >= 150 {
			t.Fatalf("耗时不应包含等待确认的时间: %d ms", r.DurationMS)
		}
	}
}

func TestMaxSteps(t *testing.T) {
	loop := fake.Step{Text: "再查一次", ToolCalls: []fake.ToolSpec{{Name: "list_dir", Input: json.RawMessage(`{}`)}}}
	script := fake.Script{Steps: []fake.Step{loop, loop, loop, loop, loop}}
	h := newHarness(t, script, 2, allow)

	res, _ := h.engine.Send(context.Background(), "无限循环")
	if res.Reason != event.TurnMaxSteps || res.Steps != 2 {
		t.Fatalf("应在 2 步后被停止: %+v", res)
	}
	errs := payloads[event.Error](h.rec)
	if len(errs) != 1 || errs[0].Code != "max_steps" {
		t.Fatalf("应发出 max_steps 错误: %+v", errs)
	}
}

func TestCancelWhileWaitingForPermission(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, func(e *core.Engine, _ event.PermissionRequest) {
		go e.Cancel() // 用户没有回答，而是点了"停止"
	})
	res, _ := h.engine.Send(context.Background(), "hi")
	if res.Reason != event.TurnCancelled {
		t.Fatalf("应为 cancelled: %+v", res)
	}
	r := payloads[event.PermissionResolved](h.rec)
	if len(r) != 1 || r[0].Decision != "deny" || r[0].Reason != "cancelled" {
		t.Fatalf("取消应按拒绝处理: %+v", r)
	}
	assertToolPairs(t, h)
	if h.engine.Status().Running {
		t.Fatal("取消后应不再运行")
	}
}

func TestProviderErrorMidStream(t *testing.T) {
	script := fake.Script{Steps: []fake.Step{{Text: "说到一半", Error: "connection reset"}}}
	h := newHarness(t, script, 0, allow)
	res, _ := h.engine.Send(context.Background(), "hi")
	if res.Reason != event.TurnError || res.Err == nil {
		t.Fatalf("应为 error: %+v", res)
	}
	errs := payloads[event.Error](h.rec)
	if len(errs) != 1 || !errs[0].Fatal || errs[0].Code != "provider_error" {
		t.Fatalf("应发出致命的 provider_error: %+v", errs)
	}
	// 已经输出的文本被保留在历史里。
	saved, _ := h.store.LoadSession(context.Background(), h.engine.Status().SessionID)
	if len(saved.Messages) != 2 || saved.Messages[1].Text() != "说到一半" {
		t.Fatalf("部分输出应被保存: %+v", saved.Messages)
	}
}

// 厂商不返回思考内容时，不应产生 thinking_delta——前端据此自然降级（不显示思考卡片）。
func TestNoThinkingDegradesGracefully(t *testing.T) {
	h := newHarness(t, fake.Script{Steps: []fake.Step{{Text: "直接回答"}}}, 0, allow)
	h.engine.Send(context.Background(), "hi")
	if slices.Contains(h.rec.Types(), event.TypeThinkingDelta) {
		t.Fatal("不应有 thinking_delta")
	}
}

func TestUnknownToolIsReportedToModel(t *testing.T) {
	script := fake.Script{Steps: []fake.Step{
		{ToolCalls: []fake.ToolSpec{{Name: "teleport", Input: json.RawMessage(`{}`)}}},
		{Text: "好吧"},
	}}
	h := newHarness(t, script, 0, allow)
	res, _ := h.engine.Send(context.Background(), "hi")
	r := payloads[event.ToolResult](h.rec)
	if res.Reason != event.TurnCompleted || len(r) != 1 || !r[0].IsError {
		t.Fatalf("未知工具应作为工具错误回传: %+v %+v", res, r)
	}
}

func TestLoadSessionReplaysHistory(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, allow)
	h.engine.Send(context.Background(), "演示")
	id := h.engine.Status().SessionID

	if _, err := h.engine.NewSession(); err != nil {
		t.Fatal(err)
	}
	if h.engine.Status().SessionID == id {
		t.Fatal("NewSession 应生成新 ID")
	}

	before := len(h.rec.Events())
	st, err := h.engine.LoadSession(context.Background(), id)
	if err != nil || st.SessionID != id {
		t.Fatalf("load: %v %+v", err, st)
	}
	replayed := h.rec.Events()[before:]
	var types []event.Type
	for _, ev := range replayed {
		types = append(types, ev.Type)
		if ev.SessionID != id {
			t.Fatalf("重放事件应带原会话 ID")
		}
	}
	types = compact(types)
	if types[0] != event.TypeTurnStart || types[len(types)-1] != event.TypeTurnEnd {
		t.Fatalf("重放应以 turn_start 开始、turn_end 结束: %v", types)
	}
	nCalls, nResults := 0, 0
	for _, ev := range replayed {
		switch p := ev.Data.(type) {
		case event.ToolCall:
			nCalls++
			if p.Risk == "" {
				t.Fatal("重放的 tool_call 应补上风险等级")
			}
		case event.ToolResult:
			nResults++
			if p.Name == "" {
				t.Fatal("重放的 tool_result 应补上工具名")
			}
		}
	}
	if nCalls != 3 || nResults != 3 {
		t.Fatalf("应重放 3 次工具调用和结果: %d %d", nCalls, nResults)
	}

	sessions, _ := h.engine.ListSessions(context.Background())
	if len(sessions) != 1 || sessions[0].Title != "演示" {
		t.Fatalf("会话列表: %+v", sessions)
	}
}

func TestSetModel(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, allow)
	if err := h.engine.SetModel("alt", ""); err != nil {
		t.Fatal(err)
	}
	if st := h.engine.Status(); st.Provider != "alt" || st.Model != "m1" {
		t.Fatalf("未指定模型时应使用第一个模型: %+v", st)
	}
	if err := h.engine.SetModel("nope", ""); err == nil {
		t.Fatal("不存在的配置档应报错")
	}
	// 切换到 alt（echo 剧本）后发送消息，应由新厂商回答。
	h.engine.Send(context.Background(), "ping")
	starts := payloads[event.MessageStart](h.rec)
	if starts[0].Provider != "alt" || starts[0].Model != "m1" {
		t.Fatalf("应使用切换后的厂商: %+v", starts[0])
	}
}

func TestSendWhileBusy(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := newHarness(t, fake.DemoScript(), 0, func(e *core.Engine, p event.PermissionRequest) {
		// 第一次权限请求时停住，制造"正在运行"的状态。
		select {
		case <-started:
		default:
			close(started)
			go func() { <-release; _ = e.RespondPermission(p.RequestID, "deny") }()
			return
		}
		_ = e.RespondPermission(p.RequestID, "deny")
	})
	done := make(chan struct{})
	go func() { h.engine.Send(context.Background(), "a"); close(done) }()
	<-started
	if _, err := h.engine.Send(context.Background(), "b"); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("运行中再次发送应返回 ErrBusy: %v", err)
	}
	if err := h.engine.SetModel("alt", ""); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("运行中切换模型应返回 ErrBusy: %v", err)
	}
	if err := h.engine.DeleteSession(context.Background(), h.engine.Status().SessionID); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("运行中删除当前会话应返回 ErrBusy: %v", err)
	}
	if !h.engine.Status().Running || len(h.engine.Status().PendingPermissions) != 1 {
		t.Fatalf("状态应显示运行中且有一个待确认请求: %+v", h.engine.Status())
	}
	close(release)
	<-done
}

// 删除当前会话后应切换到新会话，下一轮结束时不能把删掉的会话写回去。
func TestDeleteCurrentSession(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, allow)
	ctx := context.Background()
	h.engine.Send(ctx, "演示")
	id := h.engine.Status().SessionID
	if err := h.engine.DeleteSession(ctx, id); err != nil {
		t.Fatal(err)
	}
	if h.engine.Status().SessionID == id {
		t.Fatal("删除当前会话后应切换到新会话")
	}
	h.engine.Send(ctx, "演示")
	sessions, _ := h.engine.ListSessions(ctx)
	for _, s := range sessions {
		if s.ID == id {
			t.Fatal("被删除的会话不应被重新保存")
		}
	}
	if len(sessions) != 1 {
		t.Fatalf("应只剩新会话: %+v", sessions)
	}
}

// assertToolPairs 检查保存的历史里每个 tool_use 都有配对的 tool_result——否则下一次请求会被厂商拒绝。
func assertToolPairs(t *testing.T, h *harness) {
	t.Helper()
	saved, err := h.store.LoadSession(context.Background(), h.engine.Status().SessionID)
	if err != nil {
		t.Fatal(err)
	}
	pending := map[string]bool{}
	for _, m := range saved.Messages {
		for _, b := range m.Content {
			switch b.Type {
			case provider.BlockToolUse:
				pending[b.ToolCallID] = true
			case provider.BlockToolResult:
				delete(pending, b.ToolCallID)
			}
		}
	}
	if len(pending) != 0 {
		t.Fatalf("存在没有结果的工具调用: %v", pending)
	}
}

// 新目录里没有任何可用的配置档（例如删光了所有厂商）：目录照常替换，会话回到"未配置"状态，
// 发送消息时返回 ErrNoProvider；之后目录里有可用厂商了，再切换即可。
func TestReplaceCatalogWithNoUsableProfile(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, allow)
	reg := provider.NewRegistry()
	reg.Register("broken", func(provider.ProfileConfig) (provider.Provider, error) {
		return nil, errors.New("missing key")
	})
	bad := provider.NewCatalog(reg, []provider.ProfileConfig{{Name: "x", Type: "broken", Models: []string{"m"}}})
	if err := h.engine.ReplaceCatalog(bad); err != nil {
		t.Fatalf("没有可用配置档不应让替换失败: %v", err)
	}
	if st := h.engine.Status(); st.Provider != "" || st.Model != "" {
		t.Fatalf("会话应回到未配置状态: %+v", st)
	}
	if _, err := h.engine.Send(context.Background(), "演示"); !errors.Is(err, core.ErrNoProvider) {
		t.Fatalf("应返回 ErrNoProvider: %v", err)
	}
}

// 运行中切换档位：本轮里还没执行的工具调用立即按新档位处理。
func TestSetPermissionModeMidTurn(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, func(e *core.Engine, p event.PermissionRequest) {
		// 第一次弹窗时切到"全部放行"，然后允许这一次。
		if err := e.SetPermissionMode("auto"); err != nil {
			t.Error(err)
		}
		_ = e.RespondPermission(p.RequestID, "allow")
	})
	if st := h.engine.Status(); st.PermissionMode != "default" {
		t.Fatalf("初始档位应为 default: %q", st.PermissionMode)
	}
	h.engine.Send(context.Background(), "演示")
	reqs := payloads[event.PermissionRequest](h.rec)
	if len(reqs) != 1 {
		t.Fatalf("切到 auto 后不应再弹窗，实际弹了 %d 次", len(reqs))
	}
	for _, r := range payloads[event.ToolResult](h.rec) {
		if r.IsError {
			t.Fatalf("工具都应执行成功: %+v", r)
		}
	}
	if st := h.engine.Status(); st.PermissionMode != "auto" {
		t.Fatalf("status: %q", st.PermissionMode)
	}
	if err := h.engine.SetPermissionMode("autto"); err == nil {
		t.Fatal("未知档位应报错")
	}
}

func TestUpdateRuntimeAndDeleteAll(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, allow)
	h.engine.Send(context.Background(), "第一轮")
	old := h.engine.Status().SessionID

	// 默认提示词随工作区变化；切换工作区会清空"本会话总是允许"。
	ws := t.TempDir()
	if err := h.engine.UpdateRuntime(core.RuntimeConfig{Workspace: ws, MaxSteps: 3}); err != nil {
		t.Fatal(err)
	}
	if h.engine.Status().Workspace != ws {
		t.Fatal("工作区应更新")
	}
	if err := h.engine.UpdateRuntime(core.RuntimeConfig{}); err == nil {
		t.Fatal("工作区为空应报错")
	}

	n, err := h.engine.DeleteAllSessions(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("应删除 1 个会话: %d %v", n, err)
	}
	if h.engine.Status().SessionID == old {
		t.Fatal("清空后应换成新会话")
	}
	if list, _ := h.engine.ListSessions(context.Background()); len(list) != 0 {
		t.Fatalf("应已清空: %+v", list)
	}
}

func TestDisabledTools(t *testing.T) {
	script := fake.Script{Steps: []fake.Step{
		{ToolCalls: []fake.ToolSpec{{Name: "list_dir", Input: json.RawMessage(`{"path":"."}`)}}},
		{Text: "好"},
	}}
	h := newHarness(t, script, 0, allow)
	h.engine.SetDisabledTools([]string{"list_dir"})
	var found bool
	for _, ti := range h.engine.Tools() {
		if ti.Name == "list_dir" {
			found = ti.Disabled
		}
	}
	if !found {
		t.Fatal("Tools() 应标记 list_dir 已禁用")
	}
	h.engine.Send(context.Background(), "hi")
	r := payloads[event.ToolResult](h.rec)
	if len(r) != 1 || !r[0].IsError || !strings.Contains(r[0].Output, "disabled") {
		t.Fatalf("禁用的工具应返回错误给模型: %+v", r)
	}
}

func TestModelGeneratedTitle(t *testing.T) {
	// fake 对标题请求同样按剧本第 1 步回答，所以标题就是"「Go 并发入门」"去掉括号后的结果。
	h := newHarness(t, fake.Script{Steps: []fake.Step{{Text: "「Go 并发入门」\n多余的行"}}}, 0, allow)
	if err := h.engine.UpdateRuntime(core.RuntimeConfig{Workspace: h.workspace, TitleMode: "model"}); err != nil {
		t.Fatal(err)
	}
	h.engine.Send(context.Background(), "讲讲 goroutine")
	// 本轮结束时已经保存（标题先是截取的第一句），标题在后台生成。
	if list, _ := h.engine.ListSessions(context.Background()); len(list) != 1 || list[0].Title != "讲讲 goroutine" {
		t.Fatalf("应先保存会话: %+v", list)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(payloads[event.SessionUpdated](h.rec)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ups := payloads[event.SessionUpdated](h.rec)
	if len(ups) != 1 || ups[0].Title != "Go 并发入门" {
		t.Fatalf("应生成标题: %+v", ups)
	}
	list, _ := h.engine.ListSessions(context.Background())
	if len(list) != 1 || list[0].Title != "Go 并发入门" {
		t.Fatalf("标题应保存: %+v", list)
	}
	// 第二轮不再生成。
	h.engine.Send(context.Background(), "再说说 channel")
	if n := len(payloads[event.SessionUpdated](h.rec)); n != 1 {
		t.Fatalf("只在第一轮生成标题，got %d", n)
	}
}

func TestEnginePermissionRules(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, allow)
	if err := h.engine.SetPermissionRules([]permission.Rule{{Tool: "shell", Pattern: "*", Action: permission.Deny}}); err == nil {
		t.Fatal("ModePolicy 不支持规则，应报错")
	}
	if h.engine.PermissionRules() != nil {
		t.Fatal("应无规则")
	}
}

func TestContextBreakdown(t *testing.T) {
	if core.EstimateTokens("abcdefgh") != 2 || core.EstimateTokens("你好") != 2 || core.EstimateTokens("") != 0 {
		t.Fatal("EstimateTokens")
	}
	h := newHarness(t, fake.DemoScript(), 0, allow)
	before := h.engine.ContextBreakdown()
	if before.Messages != 0 || before.ToolCount != 16 || len(before.Parts) != 2 {
		t.Fatalf("空会话只有系统提示词和工具定义: %+v", before)
	}
	h.engine.Send(context.Background(), "演示一下")
	b := h.engine.ContextBreakdown()
	kinds := map[string]core.ContextPart{}
	sum := 0
	for _, p := range b.Parts {
		kinds[p.Kind] = p
		sum += p.Tokens
	}
	for _, k := range []string{"system", "tools", "user", "assistant", "thinking", "tool_call", "tool_result", "image"} {
		if kinds[k].Count == 0 {
			t.Errorf("演示剧本应产生 %s", k)
		}
	}
	if kinds["tool_call"].Count != 3 || kinds["tool_result"].Count != 3 || sum != b.Total || b.Messages == 0 {
		t.Fatalf("明细: %+v", b)
	}
	if len(b.Largest) == 0 || b.Largest[0].Tokens < b.Largest[len(b.Largest)-1].Tokens {
		t.Fatalf("最大项应按 token 数降序: %+v", b.Largest)
	}
	// 禁用工具后，工具定义的开销随之减少。
	h.engine.SetDisabledTools([]string{"shell"})
	if h.engine.ContextBreakdown().ToolCount != 15 {
		t.Fatal("禁用的工具不计入")
	}
}

func TestPerSessionWorkspace(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, allow)
	ctx := context.Background()
	if st := h.engine.Status(); !st.WorkspaceIsDefault || st.Workspace != h.workspace {
		t.Fatalf("新会话默认跟随默认工作区: %+v", st)
	}

	// 为当前会话选择工作区：工具在这个目录里读写。
	repoA := t.TempDir()
	st, err := h.engine.SetSessionWorkspace(ctx, repoA)
	if err != nil || st.Workspace != repoA || st.WorkspaceIsDefault {
		t.Fatalf("set: %+v %v", st, err)
	}
	h.engine.Send(ctx, "演示")
	if _, err := os.Stat(filepath.Join(repoA, "agent-demo", "hello.md")); err != nil {
		t.Fatalf("文件应写进会话的工作区: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.workspace, "agent-demo")); err == nil {
		t.Fatal("不应写进默认工作区")
	}
	idA := h.engine.Status().SessionID
	if list, _ := h.engine.ListSessions(ctx); len(list) != 1 || list[0].Workspace != repoA {
		t.Fatalf("会话列表应带上工作区: %+v", list)
	}

	// 新会话沿用当前会话的工作区；可以单独改。
	st, _ = h.engine.NewSession()
	if st.Workspace != repoA {
		t.Fatalf("新会话应沿用工作区: %+v", st)
	}
	repoB := t.TempDir()
	if _, err := h.engine.SetSessionWorkspace(ctx, repoB); err != nil {
		t.Fatal(err)
	}
	h.engine.Send(ctx, "另一个仓库")

	// 切回会话 A：工作区跟着会话恢复。
	st, _ = h.engine.LoadSession(ctx, idA)
	if st.Workspace != repoA {
		t.Fatalf("加载会话应恢复它的工作区: %+v", st)
	}
	// 改回默认。
	if st, _ := h.engine.SetSessionWorkspace(ctx, ""); !st.WorkspaceIsDefault || st.Workspace != h.workspace {
		t.Fatalf("应回到默认工作区: %+v", st)
	}

	// 目录被删除：状态里标出来，发送时给出明确的错误。
	gone := filepath.Join(t.TempDir(), "gone")
	os.Mkdir(gone, 0o755)
	h.engine.SetSessionWorkspace(ctx, gone)
	os.Remove(gone)
	if !h.engine.Status().WorkspaceMissing {
		t.Fatal("应标记工作区不存在")
	}
	if _, err := h.engine.Send(ctx, "hi"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("应拒绝在不存在的工作区运行: %v", err)
	}
}

func TestRenameSession(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fake.DemoScript(), 0, allow)
	if err := h.engine.RenameSession(ctx, h.engine.Status().SessionID, "  "); err == nil {
		t.Fatal("空标题应报错")
	}
	h.engine.Send(ctx, "演示一下")
	first := h.engine.Status().SessionID
	if err := h.engine.RenameSession(ctx, first, "我的会话\n"); err != nil {
		t.Fatal(err)
	}
	if ups := payloads[event.SessionUpdated](h.rec); len(ups) == 0 || ups[len(ups)-1].Title != "我的会话" {
		t.Fatalf("应发 session_updated: %+v", ups)
	}
	// 历史会话（不是当前会话）也能改名。
	if _, err := h.engine.NewSession(); err != nil {
		t.Fatal(err)
	}
	if err := h.engine.RenameSession(ctx, first, "改过两次"); err != nil {
		t.Fatal(err)
	}
	list, _ := h.engine.ListSessions(ctx)
	if len(list) != 1 || list[0].Title != "改过两次" {
		t.Fatalf("list: %+v", list)
	}
	if err := h.engine.RenameSession(ctx, "nope", "x"); err == nil {
		t.Fatal("不存在的会话应报错")
	}
}

// flaky 是测试用的厂商：失败 n 次后正常应答。
type flaky struct {
	name  string
	fails int
	calls int
}

func (f *flaky) Name() string { return f.name }
func (f *flaky) Stream(_ context.Context, _ provider.Request, on func(provider.Delta)) error {
	f.calls++
	if f.calls <= f.fails {
		return &provider.HTTPError{Status: 429, Err: errors.New("rate limited")}
	}
	on(provider.Delta{Type: provider.DeltaText, Text: "来自 " + f.name})
	on(provider.Delta{Type: provider.DeltaUsage, Usage: &provider.Usage{InputTokens: 7, OutputTokens: 3}})
	on(provider.Delta{Type: provider.DeltaStop, StopReason: provider.StopEndTurn})
	return nil
}

type usageLog struct {
	mu sync.Mutex
	ev []provider.UsageEvent
}

func (u *usageLog) RecordUsage(e provider.UsageEvent) {
	u.mu.Lock()
	u.ev = append(u.ev, e)
	u.mu.Unlock()
}

// 模型组：第一个成员被限流，自动转交给第二个；界面收到 route 事件，账本带上会话 ID。
func TestGroupFailoverEmitsRouteAndRecordsUsage(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register("flaky", func(cfg provider.ProfileConfig) (provider.Provider, error) {
		n := 0
		if cfg.Name == "a" {
			n = 99
		}
		return &flaky{name: cfg.Name, fails: n}, nil
	})
	ledger := &usageLog{}
	cat := provider.NewCatalogWith(reg, []provider.ProfileConfig{
		{Name: "a", Type: "flaky", Models: []string{"m"}, DefaultModel: "m"},
		{Name: "b", Type: "flaky", Models: []string{"m"}, DefaultModel: "m"},
		{Name: "主力", Type: provider.GroupType, Models: []string{provider.GroupModel}, DefaultModel: provider.GroupModel,
			Options: provider.GroupOptions(provider.GroupConfig{Members: []provider.GroupMember{{Profile: "a", Model: "m"}, {Profile: "b", Model: "m"}}, Strategy: provider.StrategyOrder})},
	}, provider.CatalogOptions{Recorder: ledger})
	eng, err := core.NewEngine(core.EngineConfig{
		Catalog: cat, Tools: tool.NewRegistry(), Store: store.NewMemory(), Policy: permission.NewModePolicy(permission.ModeDefault),
		Workspace: t.TempDir(), DefaultProfile: "主力", DefaultModel: provider.GroupModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := &event.Recorder{}
	eng.Bus().Subscribe(rec.Handle)

	if _, err := eng.Send(context.Background(), "你好"); err != nil {
		t.Fatal(err)
	}
	routes := payloads[event.Route](rec)
	if len(routes) != 1 || routes[0].Profile != "b" || routes[0].Try != 2 || len(routes[0].HandedOn) != 1 || routes[0].HandedOn[0].Profile != "a" || routes[0].HandedOn[0].Kind != "rate_limit" {
		t.Fatalf("route 事件: %+v", routes)
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if len(ledger.ev) != 2 || ledger.ev[0].Status != provider.StatusHandedOn || ledger.ev[1].Status != provider.StatusOK ||
		ledger.ev[1].SessionID != eng.Status().SessionID || ledger.ev[1].Usage.InputTokens != 7 || ledger.ev[1].Source != provider.SourceChat {
		t.Fatalf("账本: %+v", ledger.ev)
	}
	// 历史里只有一条应答，来自成员 b。
	var texts []string
	for _, p := range payloads[event.TextDelta](rec) {
		texts = append(texts, p.Text)
	}
	if strings.Join(texts, "") != "来自 b" {
		t.Fatalf("文本: %v", texts)
	}
}

// secretTool 是一个“读私密内容”的只读工具，用来测试 Spec.Sensitive。
type secretTool struct{ ran int }

func (s *secretTool) Spec() tool.Spec {
	return tool.Spec{Name: "read_secret", Description: "读私密内容", InputSchema: json.RawMessage(`{"type":"object"}`), Risk: permission.RiskRead, Sensitive: true}
}
func (s *secretTool) Summarize(json.RawMessage) string { return "读取私密内容" }
func (s *secretTool) Execute(context.Context, tool.Call) tool.Result {
	s.ran++
	return tool.Result{Output: "内容"}
}

func secretScript() fake.Script {
	call := fake.Step{ToolCalls: []fake.ToolSpec{{Name: "read_secret", Input: json.RawMessage(`{}`)}}}
	return fake.Script{Steps: []fake.Step{call, call, {Text: "完成"}}}
}

// 私密工具即使是只读、即使用户点过“本会话总是允许”，每次调用也都要问；请求里带 Sensitive，界面据此不提供“总是允许”。
func TestSensitiveToolAlwaysAsks(t *testing.T) {
	st := &secretTool{}
	asked := 0
	var flags []bool
	h := newHarnessWith(t, secretScript(), 0, func(e *core.Engine, p event.PermissionRequest) {
		asked++
		flags = append(flags, p.Sensitive)
		_ = e.RespondPermission(p.RequestID, "allow_session")
	}, st)
	if res, err := h.engine.Send(context.Background(), "读一下"); err != nil || res.Reason != event.TurnCompleted {
		t.Fatalf("%v %+v", err, res)
	}
	if asked != 2 || st.ran != 2 || !flags[0] || !flags[1] {
		t.Fatalf("同一轮里调两次都应该询问: asked=%d ran=%d flags=%v", asked, st.ran, flags)
	}
}

func TestSensitiveToolRespectsDeny(t *testing.T) {
	st := &secretTool{}
	h := newHarnessWith(t, secretScript(), 0, deny, st)
	h.engine.Send(context.Background(), "读一下")
	if st.ran != 0 {
		t.Fatalf("拒绝后不应执行: %d", st.ran)
	}
}
