package core

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// scriptProvider 按顺序回放：每次请求取下一步（工具调用或文字），用完之后一律回答“完成”。
type scriptProvider struct {
	mu    sync.Mutex
	steps []provider.ToolCall // Name 为空表示回一段文字（Input 当文字）
	reqs  []provider.Request
}

func (p *scriptProvider) Name() string { return "script" }

func (p *scriptProvider) Stream(ctx context.Context, req provider.Request, on func(provider.Delta)) error {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	var st *provider.ToolCall
	if len(p.steps) > 0 {
		st = &p.steps[0]
		p.steps = p.steps[1:]
	}
	p.mu.Unlock()
	switch {
	case st != nil && st.Name != "":
		c := *st
		on(provider.Delta{Type: provider.DeltaToolCall, ToolCall: &c})
		on(provider.Delta{Type: provider.DeltaStop, StopReason: provider.StopToolUse})
	case st != nil:
		on(provider.Delta{Type: provider.DeltaText, Text: string(st.Input)})
		on(provider.Delta{Type: provider.DeltaStop, StopReason: provider.StopEndTurn})
	default:
		on(provider.Delta{Type: provider.DeltaText, Text: "完成"})
		on(provider.Delta{Type: provider.DeltaStop, StopReason: provider.StopEndTurn})
	}
	return nil
}

// probeTool 记录它收到的参数。
type probeTool struct {
	name      string
	risk      permission.Risk
	sensitive bool
	mu        sync.Mutex
	inputs    []string
}

func (t *probeTool) Spec() tool.Spec {
	return tool.Spec{Name: t.name, Description: "probe", InputSchema: json.RawMessage(`{"type":"object"}`), Risk: t.risk, Sensitive: t.sensitive}
}
func (t *probeTool) Summarize(json.RawMessage) string { return t.name }
func (t *probeTool) Execute(_ context.Context, c tool.Call) tool.Result {
	t.mu.Lock()
	t.inputs = append(t.inputs, string(c.Input))
	t.mu.Unlock()
	return tool.Result{Output: "secret-output"}
}

// fakeHooks 按事件返回预设结论，并记录收到的输入。
type fakeHooks struct {
	mu  sync.Mutex
	out map[HookEvent][]HookOutcome // 每次调用取下一个；用完后返回最后一个
	got []HookInput
}

func (f *fakeHooks) Run(_ context.Context, in HookInput) HookOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, in)
	list := f.out[in.Event]
	if len(list) == 0 {
		return HookOutcome{}
	}
	o := list[0]
	if len(list) > 1 {
		f.out[in.Event] = list[1:]
	}
	o.Runs = []event.HookRun{{Source: "plugin:t", Command: "x", Outcome: "ok"}}
	return o
}

func (f *fakeHooks) inputs(ev HookEvent) []HookInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []HookInput
	for _, in := range f.got {
		if in.Event == ev {
			out = append(out, in)
		}
	}
	return out
}

// autoAnswer 是一个发布器：看到确认请求就按 decide 回答，并记下请求。
type autoAnswer struct {
	recorder
	broker *permission.Broker
	decide permission.Decision
	asked  []event.PermissionRequest
}

func (a *autoAnswer) Publish(ev event.Event) event.Event {
	a.recorder.Publish(ev)
	if pr, ok := ev.Data.(event.PermissionRequest); ok {
		a.mu.Lock()
		a.asked = append(a.asked, pr)
		a.mu.Unlock()
		go func() { _ = a.broker.Resolve(pr.RequestID, a.decide) }()
	}
	return ev
}

type hookRig struct {
	p     *scriptProvider
	tool  *probeTool
	hooks *fakeHooks
	pub   *autoAnswer
	sess  *Session
	agent *Agent
}

func newHookRig(t *testing.T, risk permission.Risk, mode permission.Mode, hooks map[HookEvent][]HookOutcome, steps ...provider.ToolCall) *hookRig {
	t.Helper()
	r := &hookRig{p: &scriptProvider{steps: steps}, tool: &probeTool{name: "probe", risk: risk}, hooks: &fakeHooks{out: hooks}}
	reg := tool.NewRegistry()
	if err := reg.Register(r.tool); err != nil {
		t.Fatal(err)
	}
	broker := permission.NewBroker()
	r.pub = &autoAnswer{broker: broker, decide: permission.DecisionAllowSession}
	r.sess = NewSession("s1", "script", "m")
	r.agent = NewAgent(AgentConfig{
		Provider: r.p, Model: "m", Publisher: r.pub, SessionID: "s1", Workspace: t.TempDir(),
		Tools: reg, Policy: permission.NewModePolicy(mode), Broker: broker, Hooks: r.hooks,
	})
	return r
}

func callProbe(input string) provider.ToolCall {
	return provider.ToolCall{ID: "c1", Name: "probe", Input: json.RawMessage(input)}
}

func lastToolResult(t *testing.T, s *Session) provider.Block {
	t.Helper()
	msgs := s.Snapshot().Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		for _, b := range msgs[i].Content {
			if b.Type == provider.BlockToolResult {
				return b
			}
		}
	}
	t.Fatal("没有工具结果")
	return provider.Block{}
}

func TestHookPreToolUse(t *testing.T) {
	t.Run("block：不执行，原因告诉模型，事件挂在工具卡片上", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, map[HookEvent][]HookOutcome{HookPreToolUse: {{Block: true, Reason: "不许"}}}, callProbe(`{"a":1}`))
		res := r.agent.Run(context.Background(), r.sess, "go")
		if res.Reason != event.TurnCompleted || len(r.tool.inputs) != 0 {
			t.Fatalf("应当没执行: %+v %v", res, r.tool.inputs)
		}
		if b := lastToolResult(t, r.sess); !b.IsError || !strings.Contains(b.Text, "不许") {
			t.Fatalf("%+v", b)
		}
		runs := eventsOf[event.HookRun](&r.pub.recorder)
		if len(runs) == 0 || runs[0].CallID != "c1" || runs[0].Hook != "PreToolUse" {
			t.Fatalf("hook_run 事件: %+v", runs)
		}
		if in := r.hooks.inputs(HookPreToolUse); len(in) != 1 || in[0].ToolName != "probe" || string(in[0].ToolInput) != `{"a":1}` || in[0].SessionID != "s1" {
			t.Fatalf("钩子输入: %+v", in)
		}
	})
	t.Run("allow 不跳过确认", func(t *testing.T) {
		// 钩子的 allow 由 internal/hooks 忽略（不会变成 HookOutcome 的任何字段）；这里确认没有表态时照常确认。
		r := newHookRig(t, permission.RiskWrite, permission.ModeDefault, map[HookEvent][]HookOutcome{HookPreToolUse: {{}}}, callProbe(`{}`))
		r.agent.Run(context.Background(), r.sess, "go")
		if len(r.pub.asked) != 1 || len(r.tool.inputs) != 1 {
			t.Fatalf("写工具仍要确认: asked=%d ran=%d", len(r.pub.asked), len(r.tool.inputs))
		}
	})
	t.Run("ask 把放行提成确认，且回答不记成本会话总是允许", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, map[HookEvent][]HookOutcome{HookPreToolUse: {{Ask: true}}}, callProbe(`{}`), callProbe(`{}`))
		r.agent.Run(context.Background(), r.sess, "go")
		if len(r.pub.asked) != 2 || len(r.tool.inputs) != 2 {
			t.Fatalf("只读工具在钩子要求时每次都要确认: asked=%d ran=%d", len(r.pub.asked), len(r.tool.inputs))
		}
	})
	t.Run("ask 不能把拒绝降成确认", func(t *testing.T) {
		r := newHookRig(t, permission.RiskWrite, permission.ModeReadOnly, map[HookEvent][]HookOutcome{HookPreToolUse: {{Ask: true}}}, callProbe(`{}`))
		r.agent.Run(context.Background(), r.sess, "go")
		if len(r.pub.asked) != 0 || len(r.tool.inputs) != 0 {
			t.Fatalf("只读模式下写工具仍然拒绝: asked=%d ran=%d", len(r.pub.asked), len(r.tool.inputs))
		}
	})
	t.Run("updatedInput 在确认之前生效：弹窗和执行都用改后的参数", func(t *testing.T) {
		r := newHookRig(t, permission.RiskWrite, permission.ModeDefault, map[HookEvent][]HookOutcome{HookPreToolUse: {{UpdatedInput: json.RawMessage(`{"a":2}`)}}}, callProbe(`{"a":1}`))
		r.agent.Run(context.Background(), r.sess, "go")
		if len(r.pub.asked) != 1 || string(r.pub.asked[0].Input) != `{"a":2}` || r.tool.inputs[0] != `{"a":2}` {
			t.Fatalf("asked=%+v ran=%v", r.pub.asked, r.tool.inputs)
		}
	})
	t.Run("continue:false 停下整轮", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, map[HookEvent][]HookOutcome{HookPreToolUse: {{StopTurn: true, StopReason: "够了"}}}, callProbe(`{}`), callProbe(`{}`))
		res := r.agent.Run(context.Background(), r.sess, "go")
		if res.Reason != event.TurnCompleted || len(r.tool.inputs) != 0 || len(r.p.reqs) != 1 {
			t.Fatalf("应当在第一次工具调用后停下: %+v ran=%v reqs=%d", res, r.tool.inputs, len(r.p.reqs))
		}
		errs := eventsOf[event.Error](&r.pub.recorder)
		if len(errs) == 0 || errs[0].Code != "hook_stopped" || errs[0].Fatal {
			t.Fatalf("%+v", errs)
		}
	})
}

func TestHookPostToolUse(t *testing.T) {
	t.Run("工具输出交给钩子；反馈和上下文带标注加在结果后面", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, map[HookEvent][]HookOutcome{HookPostToolUse: {{Block: true, Reason: "格式不对", Context: []string{"补充"}}}}, callProbe(`{}`))
		r.agent.Run(context.Background(), r.sess, "go")
		in := r.hooks.inputs(HookPostToolUse)
		if len(in) != 1 || !in[0].HasToolResponse || in[0].ToolResponse != "secret-output" {
			t.Fatalf("%+v", in)
		}
		b := lastToolResult(t, r.sess)
		if !strings.HasPrefix(b.Text, "secret-output") || !strings.Contains(b.Text, "格式不对") || !strings.Contains(b.Text, "补充") || !strings.Contains(b.Text, "来自插件钩子") {
			t.Fatalf("%q", b.Text)
		}
	})
	t.Run("私密工具的输出不交给钩子", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, nil, callProbe(`{}`))
		r.tool.sensitive = true
		r.agent.Run(context.Background(), r.sess, "go")
		in := r.hooks.inputs(HookPostToolUse)
		if len(in) != 1 || in[0].HasToolResponse || in[0].ToolResponse != "" {
			t.Fatalf("私密输出泄露给钩子: %+v", in)
		}
	})
	t.Run("上下文有上限", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, map[HookEvent][]HookOutcome{HookPostToolUse: {{Context: []string{strings.Repeat("长", 5000)}}}}, callProbe(`{}`))
		r.agent.Run(context.Background(), r.sess, "go")
		b := lastToolResult(t, r.sess)
		if n := strings.Count(b.Text, "长"); n != maxHookContext || !strings.Contains(b.Text, "已截断") {
			t.Fatalf("应截到 %d 字: %d", maxHookContext, n)
		}
	})
}

func TestHookUserPromptSubmit(t *testing.T) {
	t.Run("block：不进历史、不调用模型", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, map[HookEvent][]HookOutcome{HookUserPromptSubmit: {{Block: true, Reason: "含密钥"}}})
		res := r.agent.Run(context.Background(), r.sess, "我的 key 是 xxx")
		if res.Reason != event.TurnCompleted || len(r.p.reqs) != 0 || len(r.sess.Snapshot().Messages) != 0 {
			t.Fatalf("%+v reqs=%d msgs=%d", res, len(r.p.reqs), len(r.sess.Snapshot().Messages))
		}
		if in := r.hooks.inputs(HookUserPromptSubmit); len(in) != 1 || in[0].Prompt != "我的 key 是 xxx" {
			t.Fatalf("%+v", in)
		}
		errs := eventsOf[event.Error](&r.pub.recorder)
		if len(errs) != 1 || errs[0].Code != "hook_blocked" || !strings.Contains(errs[0].Message, "含密钥") {
			t.Fatalf("%+v", errs)
		}
		if len(eventsOf[event.TurnEnd](&r.pub.recorder)) != 1 {
			t.Fatal("一定要有 turn_end，界面才能解锁输入框")
		}
	})
	t.Run("上下文带标注附在这条输入后面", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, map[HookEvent][]HookOutcome{HookUserPromptSubmit: {{Context: []string{"今天是周日"}}}})
		r.agent.Run(context.Background(), r.sess, "hi")
		first := r.sess.Snapshot().Messages[0]
		if len(first.Content) != 2 || first.Content[0].Text != "hi" || !strings.Contains(first.Content[1].Text, "今天是周日") || !strings.Contains(first.Content[1].Text, "不是用户说的话") {
			t.Fatalf("%+v", first.Content)
		}
	})
}

func TestHookStop(t *testing.T) {
	t.Run("block 让 agent 接着做，最多 3 次", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, map[HookEvent][]HookOutcome{HookStop: {{Block: true, Reason: "测试还没跑"}}})
		res := r.agent.Run(context.Background(), r.sess, "go")
		in := r.hooks.inputs(HookStop)
		if res.Reason != event.TurnCompleted || len(in) != maxStopContinues+1 || len(r.p.reqs) != maxStopContinues+1 {
			t.Fatalf("%+v stops=%d reqs=%d", res, len(in), len(r.p.reqs))
		}
		if in[0].StopHookActive || !in[1].StopHookActive || in[0].LastAssistantMessage != "完成" {
			t.Fatalf("%+v", in)
		}
	})
	t.Run("子 agent 跑的是 SubagentStop（带名字），不是 Stop / UserPromptSubmit", func(t *testing.T) {
		r := newHookRig(t, permission.RiskRead, permission.ModeDefault, map[HookEvent][]HookOutcome{HookSubagentStop: {{Block: true, Reason: "再查一下"}, {}}})
		r.agent.cfg.ParentID = "main"
		r.agent.cfg.ID = "agent_x"
		r.agent.cfg.AgentType = "explorer"
		r.agent.RunSubagent(context.Background(), r.sess, "go")
		if len(r.hooks.inputs(HookStop)) != 0 || len(r.hooks.inputs(HookUserPromptSubmit)) != 0 {
			t.Fatal("子 agent 不应触发 Stop / UserPromptSubmit")
		}
		in := r.hooks.inputs(HookSubagentStop)
		if len(in) != 2 || in[0].AgentType != "explorer" || in[0].AgentID != "agent_x" || !in[1].StopHookActive || len(r.p.reqs) != 2 {
			t.Fatalf("SubagentStop 的 block 让子 agent 接着做一次: %+v reqs=%d", in, len(r.p.reqs))
		}
	})
}
