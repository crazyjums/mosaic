package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/provider/fake"
)

// saveLongSession 往存储里放一个 n 轮的会话：每轮 = 用户发言 + 一次工具调用 + 工具结果 + 最终回答。
func saveLongSession(t *testing.T, h *harness, n int) string {
	t.Helper()
	s := core.NewSession("sess_long", "fake", "demo")
	for i := 0; i < n; i++ {
		call := fmt.Sprintf("call_%d", i)
		s.Messages = append(s.Messages,
			provider.UserText(fmt.Sprintf("问题 %d", i)),
			provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockToolUse, ToolCallID: call, ToolName: "list_dir", ToolInput: []byte(`{}`)}}},
			provider.Message{Role: provider.RoleUser, Content: []provider.Block{{Type: provider.BlockToolResult, ToolCallID: call, Text: "ok"}}},
			provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockText, Text: fmt.Sprintf("回答 %d", i)}}},
		)
	}
	if err := h.store.SaveSession(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func userTexts(evs []event.Event) []string {
	var out []string
	for _, ev := range evs {
		if p, ok := ev.Data.(event.UserMessage); ok {
			out = append(out, p.Text)
		}
	}
	return out
}

func TestLoadSessionTailAndEarlier(t *testing.T) {
	h := newHarness(t, fake.Script{}, 0, allow)
	id := saveLongSession(t, h, 25)
	before := len(h.rec.Events())

	got, err := h.engine.LoadSessionTail(context.Background(), id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.SessionID != id || got.Earlier != 15 {
		t.Fatalf("应只加载最近 10 轮，还剩 15 轮: %+v earlier=%d", got.Status, got.Earlier)
	}
	if u := userTexts(got.Events); len(u) != 10 || u[0] != "问题 15" || u[9] != "问题 24" {
		t.Fatalf("最近 10 轮: %v", u)
	}
	if len(h.rec.Events()) != before {
		t.Fatal("尾部加载的事件不应发布到总线")
	}
	for _, ev := range got.Events {
		if ev.ID == "" || ev.Version != event.ProtocolVersion || ev.SessionID != id {
			t.Fatalf("信封应补全: %+v", ev)
		}
	}

	// 更早的一页：接在前面，不重不漏。
	page, err := h.engine.LoadEarlier(id, 10)
	if err != nil || page.Earlier != 5 {
		t.Fatalf("第二页: %+v %v", page, err)
	}
	if u := userTexts(page.Events); len(u) != 10 || u[0] != "问题 5" || u[9] != "问题 14" {
		t.Fatalf("第二页内容: %v", u)
	}
	last, err := h.engine.LoadEarlier(id, 10)
	if err != nil || last.Earlier != 0 || len(userTexts(last.Events)) != 5 {
		t.Fatalf("最后一页应只剩 5 轮: %+v %v", last, err)
	}
	// 到头之后再请求：空结果，不是错误。
	if end, err := h.engine.LoadEarlier(id, 10); err != nil || len(end.Events) != 0 || end.Earlier != 0 {
		t.Fatalf("到头了: %+v %v", end, err)
	}
	// 事件里的 tool_result 都带着工具名（说明每页里 tool_call 和 tool_result 没有被切开）。
	for _, ev := range append(page.Events, last.Events...) {
		if r, ok := ev.Data.(event.ToolResult); ok && r.Name == "" {
			t.Fatal("分页把 tool_call 和 tool_result 切开了")
		}
	}
}

func TestLoadSessionTailShortSession(t *testing.T) {
	h := newHarness(t, fake.Script{}, 0, allow)
	id := saveLongSession(t, h, 3)
	got, err := h.engine.LoadSessionTail(context.Background(), id, 0) // 0 = 默认 20 轮
	if err != nil || got.Earlier != 0 || len(userTexts(got.Events)) != 3 {
		t.Fatalf("短会话应全部加载: %+v %v", got.Earlier, err)
	}
}

func TestLoadEarlierRejectsStaleWindow(t *testing.T) {
	h := newHarness(t, fake.Script{}, 0, allow)
	id := saveLongSession(t, h, 5)
	if _, err := h.engine.LoadSessionTail(context.Background(), id, 2); err != nil {
		t.Fatal(err)
	}
	// 切到别的会话后，旧会话的分页请求要失败，而不是返回当前会话的内容。
	if _, err := h.engine.NewSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.LoadEarlier(id, 2); !errors.Is(err, core.ErrHistoryChanged) {
		t.Fatalf("应返回 ErrHistoryChanged: %v", err)
	}
}

// 会话记录的厂商已经不存在、而且一个厂商都没有时：打开会话后回到"未配置"状态（而不是停在一个不存在的厂商上），
// 发送消息得到明确的 ErrNoProvider。
func TestOpenSessionWithoutAnyProviderIsUnset(t *testing.T) {
	h := newHarness(t, fake.Script{}, 0, allow)
	id := saveLongSession(t, h, 2)
	none := provider.NewCatalog(provider.NewRegistry(), nil)
	if err := h.engine.ReplaceCatalog(none); err != nil {
		t.Fatal(err)
	}
	got, err := h.engine.LoadSessionTail(context.Background(), id, 5)
	if err != nil || got.Status.Provider != "" {
		t.Fatalf("应回到未配置状态: %+v %v", got.Status, err)
	}
	if _, err := h.engine.Send(context.Background(), "hi"); !errors.Is(err, core.ErrNoProvider) {
		t.Fatalf("应返回 ErrNoProvider: %v", err)
	}
}

// captureProvider 记录收到的系统提示词，然后立即结束。
type captureProvider struct{ system *[]string }

func (c captureProvider) Name() string { return "cap" }
func (c captureProvider) Stream(_ context.Context, req provider.Request, onDelta func(provider.Delta)) error {
	*c.system = append(*c.system, req.System)
	onDelta(provider.Delta{Type: provider.DeltaText, Text: "ok"})
	onDelta(provider.Delta{Type: provider.DeltaStop, StopReason: provider.StopEndTurn})
	return nil
}

// 装配层提供的技能清单会追加到系统提示词后面，并且按会话的工作区生成。
func TestSkillsPromptIsAppendedPerWorkspace(t *testing.T) {
	var systems, workspaces []string
	reg := provider.NewRegistry()
	reg.Register("cap", func(provider.ProfileConfig) (provider.Provider, error) { return captureProvider{&systems}, nil })
	ws := t.TempDir()
	eng, err := core.NewEngine(core.EngineConfig{
		Catalog:      provider.NewCatalog(reg, []provider.ProfileConfig{{Name: "cap", Type: "cap", Models: []string{"m"}}}),
		Workspace:    ws,
		SystemPrompt: "BASE",
		SkillsPrompt: func(w string) string {
			workspaces = append(workspaces, w)
			return "\n\nSkills: demo"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if len(systems) != 1 || systems[0] != "BASE\n\nSkills: demo" {
		t.Fatalf("系统提示词应追加技能清单: %q", systems)
	}
	if workspaces[0] != ws {
		t.Fatalf("应按会话的工作区生成: %v", workspaces)
	}
}
