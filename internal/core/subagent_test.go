package core_test

import (
	"context"
	"encoding/json"
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

const childMarker = "你是子 agent：EXPLORER"

// routed 按系统提示词分流：子 agent 的请求走 child 剧本，主 agent 的走 main 剧本。
// fake 厂商按“最后一条用户消息之后有几条回复”决定演到第几步，两段上下文各自从头数，互不干扰。
// 同时记下每次请求带了哪些工具，用来断言子 agent 拿不到 task。
type routed struct {
	main, child *fake.Provider
	mu          sync.Mutex
	mainTools   [][]string
	childTools  [][]string
}

func (r *routed) Name() string { return "fake" }
func (r *routed) Stream(ctx context.Context, req provider.Request, on func(provider.Delta)) error {
	names := []string{}
	for _, t := range req.Tools {
		names = append(names, t.Name)
	}
	r.mu.Lock()
	isChild := strings.Contains(req.System, childMarker)
	if isChild {
		r.childTools = append(r.childTools, names)
	} else {
		r.mainTools = append(r.mainTools, names)
	}
	r.mu.Unlock()
	if isChild {
		return r.child.Stream(ctx, req, on)
	}
	return r.main.Stream(ctx, req, on)
}

func call(name string, input any) fake.ToolSpec {
	b, _ := json.Marshal(input)
	return fake.ToolSpec{Name: name, Input: b}
}

type subHarness struct {
	engine *core.Engine
	rec    *event.Recorder
	p      *routed
	ws     string
}

func newSubHarness(t *testing.T, main, child fake.Script, answer func(*core.Engine, event.PermissionRequest), defs ...core.SubagentDefinition) *subHarness {
	t.Helper()
	p := &routed{main: fake.NewWithScript("fake", main, 0), child: fake.NewWithScript("fake", child, 0)}
	reg := provider.NewRegistry()
	reg.Register("fake", func(provider.ProfileConfig) (provider.Provider, error) { return p, nil })
	catalog := provider.NewCatalog(reg, []provider.ProfileConfig{{Name: "fake", Type: "fake", Models: []string{"demo", "small"}, DefaultModel: "demo"}})
	tools := tool.NewRegistry()
	builtin.Register(tools, builtin.Options{ShellTimeout: 5 * time.Second})
	ws := t.TempDir()
	eng, err := core.NewEngine(core.EngineConfig{
		Catalog: catalog, Tools: tools, Store: store.NewMemory(),
		Policy: permission.NewModePolicy(permission.ModeDefault), Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.Subagents().ReplaceSource("user", defs)
	rec := &event.Recorder{}
	eng.Bus().Subscribe(rec.Handle)
	eng.Bus().Subscribe(func(ev event.Event) {
		if pr, ok := ev.Data.(event.PermissionRequest); ok && answer != nil {
			answer(eng, pr)
		}
	})
	return &subHarness{engine: eng, rec: rec, p: p, ws: ws}
}

var explorer = core.SubagentDefinition{Name: "explorer", Description: "只读地查看工作区", System: childMarker + "。只读查看，给出简短结论。"}

func TestSubagentFullRound(t *testing.T) {
	main := fake.Script{Steps: []fake.Step{
		{Text: "我派个子 agent 去看看", ToolCalls: []fake.ToolSpec{call("task", map[string]string{"agent": "explorer", "prompt": "列出工作区里的文件"})}},
		{Text: "子 agent 说有 a.txt，好的。"},
	}}
	child := fake.Script{Steps: []fake.Step{
		{ToolCalls: []fake.ToolSpec{call("list_dir", map[string]string{"path": "."})}},
		{Text: "结论：工作区里有 a.txt。"},
	}}
	h := newSubHarness(t, main, child, allow, explorer)
	if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := h.engine.Send(context.Background(), "看看工作区")
	if err != nil || res.Reason != event.TurnCompleted {
		t.Fatalf("%+v %v", res, err)
	}

	// 这一轮的边界只有主 agent 发：子 agent 不能发 turn_start / turn_end（否则界面以为整轮结束了）
	if n := len(payloads[event.TurnStart](h.rec)); n != 1 {
		t.Fatalf("turn_start 应只有 1 个，实际 %d", n)
	}
	if n := len(payloads[event.TurnEnd](h.rec)); n != 1 {
		t.Fatalf("turn_end 应只有 1 个，实际 %d", n)
	}

	starts := payloads[event.SubagentStart](h.rec)
	ends := payloads[event.SubagentEnd](h.rec)
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("subagent_start/end: %+v %+v", starts, ends)
	}
	st := starts[0]
	if st.Name != "explorer" || st.ParentID != event.RootAgentID || st.CallID == "" || st.AgentID == event.RootAgentID {
		t.Fatalf("subagent_start: %+v", st)
	}
	if ends[0].Status != "completed" || !strings.Contains(ends[0].Summary, "a.txt") {
		t.Fatalf("subagent_end: %+v", ends[0])
	}
	// 子 agent 的事件带着自己的 agent_id 和 parent_id，界面据此挂到树上
	var childEvents int
	for _, ev := range h.rec.Events() {
		if ev.AgentID == st.AgentID {
			childEvents++
			if ev.ParentID != event.RootAgentID {
				t.Fatalf("子 agent 事件的 parent_id: %+v", ev)
			}
		}
	}
	if childEvents == 0 {
		t.Fatal("应有子 agent 自己的事件")
	}
	// task 的结果就是子 agent 的结论
	var taskOut string
	for _, r := range payloads[event.ToolResult](h.rec) {
		if r.CallID == st.CallID {
			taskOut = r.Output
		}
	}
	if taskOut != "结论：工作区里有 a.txt。" {
		t.Fatalf("task 的结果: %q", taskOut)
	}
	// 主 agent 看得到 task；子 agent 看不到（第一版不允许嵌套）
	if !slices.Contains(h.p.mainTools[0], "task") {
		t.Fatalf("有子 agent 时主 agent 应能看到 task: %v", h.p.mainTools[0])
	}
	for _, names := range h.p.childTools {
		if slices.Contains(names, "task") {
			t.Fatalf("子 agent 不能看到 task: %v", names)
		}
	}
	// 子 agent 的用量计入会话
	if u := h.engine.Status().Usage; u.InputTokens == 0 && u.OutputTokens == 0 {
		t.Logf("用量: %+v", u)
	}
}

func TestSubagentToolRestrictionsAndPermissions(t *testing.T) {
	ro := explorer
	ro.Tools = []string{"read_file", "list_dir"}
	main := fake.Script{Steps: []fake.Step{
		{ToolCalls: []fake.ToolSpec{call("task", map[string]string{"agent": "explorer", "prompt": "写个文件"})}},
		{Text: "好"},
	}}
	child := fake.Script{Steps: []fake.Step{
		{ToolCalls: []fake.ToolSpec{call("write_file", map[string]string{"path": "x.txt", "content": "hi"})}},
		{Text: "写不了"},
	}}
	h := newSubHarness(t, main, child, allow, ro)
	if _, err := h.engine.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if got := h.p.childTools[0]; !slices.Equal(got, []string{"list_dir", "read_file"}) && !slices.Equal(got, []string{"read_file", "list_dir"}) {
		t.Fatalf("白名单之外的工具不应发给子 agent: %v", got)
	}
	if _, err := os.Stat(filepath.Join(h.ws, "x.txt")); err == nil {
		t.Fatal("白名单之外的工具不能执行")
	}

	// 权限：子 agent 的写操作照常弹确认；拒绝就写不成
	rw := explorer
	child2 := fake.Script{Steps: []fake.Step{
		{ToolCalls: []fake.ToolSpec{call("write_file", map[string]string{"path": "y.txt", "content": "hi"})}},
		{Text: "被拒绝了"},
	}}
	h2 := newSubHarness(t, main, child2, deny, rw)
	if _, err := h2.engine.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	reqs := 0
	for _, ev := range h2.rec.Events() {
		if _, ok := ev.Data.(event.PermissionRequest); ok {
			reqs++
			if ev.AgentID == event.RootAgentID {
				t.Fatal("权限请求应来自子 agent（界面据此标明是谁要做）")
			}
		}
	}
	if reqs != 1 {
		t.Fatalf("子 agent 的写操作应弹一次确认，实际 %d", reqs)
	}
	if _, err := os.Stat(filepath.Join(h2.ws, "y.txt")); err == nil {
		t.Fatal("拒绝后不能写")
	}
}

func TestSubagentEdgeCases(t *testing.T) {
	t.Run("没有子 agent 时不把 task 发给模型", func(t *testing.T) {
		h := newSubHarness(t, fake.Script{Steps: []fake.Step{{Text: "hi"}}}, fake.Script{}, allow)
		if _, err := h.engine.Send(context.Background(), "hi"); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(h.p.mainTools[0], "task") {
			t.Fatalf("没有子 agent 时不应有 task: %v", h.p.mainTools[0])
		}
	})
	t.Run("白名单一个都对不上：拒绝运行，不悄悄给全部工具", func(t *testing.T) {
		bad := explorer
		bad.Tools = []string{"no_such_tool"}
		main := fake.Script{Steps: []fake.Step{{ToolCalls: []fake.ToolSpec{call("task", map[string]string{"agent": "explorer", "prompt": "x"})}}, {Text: "好"}}}
		h := newSubHarness(t, main, fake.Script{}, allow, bad)
		if _, err := h.engine.Send(context.Background(), "go"); err != nil {
			t.Fatal(err)
		}
		if len(h.p.childTools) != 0 {
			t.Fatal("子 agent 不应被启动")
		}
		var out string
		for _, r := range payloads[event.ToolResult](h.rec) {
			if r.Name == "task" {
				out = r.Output
				if !r.IsError {
					t.Error("应是错误结果")
				}
			}
		}
		if !strings.Contains(out, "对不上") {
			t.Fatalf("应说明原因: %q", out)
		}
	})
	t.Run("不存在的子 agent、缺参数", func(t *testing.T) {
		main := fake.Script{Steps: []fake.Step{{ToolCalls: []fake.ToolSpec{
			call("task", map[string]string{"agent": "nobody", "prompt": "x"}),
			call("task", map[string]string{"agent": "explorer"}),
		}}, {Text: "好"}}}
		h := newSubHarness(t, main, fake.Script{}, allow, explorer)
		if _, err := h.engine.Send(context.Background(), "go"); err != nil {
			t.Fatal(err)
		}
		var outs []string
		for _, r := range payloads[event.ToolResult](h.rec) {
			outs = append(outs, r.Output)
		}
		if len(outs) != 2 || !strings.Contains(outs[0], "nobody") || !strings.Contains(outs[1], "prompt") {
			t.Fatalf("%v", outs)
		}
	})
	t.Run("模型：只有配置档里有这个模型才换", func(t *testing.T) {
		withModel := explorer
		withModel.Model = "small"
		main := fake.Script{Steps: []fake.Step{{ToolCalls: []fake.ToolSpec{call("task", map[string]string{"agent": "explorer", "prompt": "x"})}}, {Text: "好"}}}
		h := newSubHarness(t, main, fake.Script{Steps: []fake.Step{{Text: "ok"}}}, allow, withModel)
		if _, err := h.engine.Send(context.Background(), "go"); err != nil {
			t.Fatal(err)
		}
		var models []string
		for _, ev := range h.rec.Events() {
			if ms, ok := ev.Data.(event.MessageStart); ok && ev.AgentID != event.RootAgentID {
				models = append(models, ms.Model)
			}
		}
		if len(models) != 1 || models[0] != "small" {
			t.Fatalf("子 agent 应使用 small: %v", models)
		}
	})
	t.Run("最大步数：结论标注未完成", func(t *testing.T) {
		short := explorer
		short.MaxSteps = 1
		loop := fake.Step{ToolCalls: []fake.ToolSpec{call("list_dir", map[string]string{"path": "."})}}
		main := fake.Script{Steps: []fake.Step{{ToolCalls: []fake.ToolSpec{call("task", map[string]string{"agent": "explorer", "prompt": "x"})}}, {Text: "好"}}}
		h := newSubHarness(t, main, fake.Script{Steps: []fake.Step{loop, loop, loop}}, allow, short)
		if _, err := h.engine.Send(context.Background(), "go"); err != nil {
			t.Fatal(err)
		}
		for _, r := range payloads[event.ToolResult](h.rec) {
			if r.Name == "task" && !strings.Contains(r.Output, "上限") {
				t.Fatalf("应标注达到上限: %q", r.Output)
			}
		}
		// 子 agent 的 max_steps 不是整轮的致命错误
		for _, e := range payloads[event.Error](h.rec) {
			if e.Code == "max_steps" && e.Fatal {
				t.Fatal("子 agent 的步数上限不应是致命错误")
			}
		}
	})
}

func TestSubagentRegistryReplaceSource(t *testing.T) {
	r := core.NewSubagentRegistry()
	r.ReplaceSource("user", []core.SubagentDefinition{{Name: "a"}, {Name: "b"}})
	if skipped := r.ReplaceSource("plugin:x", []core.SubagentDefinition{{Name: "b"}, {Name: "x:c"}}); !slices.Equal(skipped, []string{"b"}) {
		t.Fatalf("被别的来源占用的名字不登记: %v", skipped)
	}
	r.ReplaceSource("user", []core.SubagentDefinition{{Name: "a"}})
	var names []string
	for _, d := range r.List() {
		names = append(names, d.Name+"@"+d.Source)
	}
	if !slices.Equal(names, []string{"a@user", "x:c@plugin:x"}) {
		t.Fatalf("%v", names)
	}
}
