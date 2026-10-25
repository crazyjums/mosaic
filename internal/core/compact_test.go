package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// stubProvider 同时扮演"聊天模型"和"摘要模型"：用请求的 System 区分两种调用。
type stubProvider struct {
	mu          sync.Mutex
	summary     string
	summaryErr  error
	summaryHook func(ctx context.Context) error
	summaryReqs []provider.Request
	chatReqs    []provider.Request
}

func (p *stubProvider) Name() string { return "stub" }

func (p *stubProvider) Stream(ctx context.Context, req provider.Request, on func(provider.Delta)) error {
	p.mu.Lock()
	isSummary := req.System == summaryInstruction
	if isSummary {
		p.summaryReqs = append(p.summaryReqs, req)
	} else {
		p.chatReqs = append(p.chatReqs, req)
	}
	p.mu.Unlock()
	if isSummary {
		if p.summaryHook != nil {
			if err := p.summaryHook(ctx); err != nil {
				return err
			}
		}
		if p.summaryErr != nil {
			return p.summaryErr
		}
		on(provider.Delta{Type: provider.DeltaText, Text: p.summary})
		return nil
	}
	on(provider.Delta{Type: provider.DeltaText, Text: "好的"})
	on(provider.Delta{Type: provider.DeltaStop, StopReason: provider.StopEndTurn})
	return nil
}

func txt(role provider.Role, s string) provider.Message {
	return provider.Message{Role: role, Content: []provider.Block{{Type: provider.BlockText, Text: s}}}
}

func call(id, name string) provider.Message {
	return provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockToolUse, ToolCallID: id, ToolName: name, ToolInput: json.RawMessage(`{"path":"a.go"}`)}}}
}

func result(id, s string) provider.Message {
	return provider.Message{Role: provider.RoleUser, Content: []provider.Block{{Type: provider.BlockToolResult, ToolCallID: id, Text: s}}}
}

// bigHistory：n 轮对话，每轮 "问 → 调工具 → 工具结果 → 答"，工具结果很大。
func bigHistory(n int, resultSize int) []provider.Message {
	var m []provider.Message
	for i := 0; i < n; i++ {
		id := "c" + string(rune('a'+i))
		m = append(m, txt(provider.RoleUser, "问题"+id), call(id, "read_file"), result(id, strings.Repeat("x", resultSize)), txt(provider.RoleAssistant, "回答"+id))
	}
	return m
}

// assertPaired 断言 tool_use / tool_result 一一对应，且 tool_result 总在对应 tool_use 之后。
func assertPaired(t *testing.T, msgs []provider.Message) {
	t.Helper()
	open := map[string]bool{}
	for i, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case provider.BlockToolUse:
				open[b.ToolCallID] = true
			case provider.BlockToolResult:
				if !open[b.ToolCallID] {
					t.Fatalf("消息 %d 的 tool_result(%s) 没有对应的 tool_use（被拆开了）", i, b.ToolCallID)
				}
				delete(open, b.ToolCallID)
			}
		}
	}
	if len(open) != 0 {
		t.Fatalf("有 tool_use 没有结果: %v", open)
	}
}

func newCompactor(p *stubProvider, window int) *SummaryCompactor {
	return &SummaryCompactor{Provider: p, Model: "m", Window: window}
}

func TestCompactNoopWhenUnsupportedOrSmall(t *testing.T) {
	p := &stubProvider{summary: "S"}
	msgs := bigHistory(6, 4000)

	// 不知道窗口：不压缩（也不调用模型）。
	r, err := newCompactor(p, 0).Compact(context.Background(), CompactRequest{Messages: msgs})
	if err != nil || r.Changed || len(p.summaryReqs) != 0 {
		t.Fatalf("窗口未知不应压缩: %v %+v", err, r)
	}
	// 远没到阈值：不压缩。
	r, err = newCompactor(p, 1_000_000).Compact(context.Background(), CompactRequest{Messages: msgs})
	if err != nil || r.Changed || len(p.summaryReqs) != 0 {
		t.Fatalf("未达阈值不应压缩: %v %+v", err, r)
	}
}

func TestCompactKeepsRecentTurnsAndPairs(t *testing.T) {
	p := &stubProvider{summary: "目标：读文件。已读 a.go。"}
	msgs := bigHistory(6, 4000) // 约 6×1000+ tokens
	before := estimateMessages(msgs)
	c := newCompactor(p, before*10/9) // 占用 > 80% 窗口

	r, err := c.Compact(context.Background(), CompactRequest{Messages: msgs, SessionID: "s1"})
	if err != nil || !r.Changed {
		t.Fatalf("应压缩: %v %+v", err, r)
	}
	if !r.Messages[0].IsSummary() || r.Messages[0].IsUserInput() {
		t.Fatalf("第一条应是摘要，且不算用户发言: %+v", r.Messages[0])
	}
	if !strings.Contains(r.Messages[0].SummaryText(), "目标：读文件") || r.Summary == "" {
		t.Fatalf("摘要正文: %q", r.Messages[0].SummaryText())
	}
	// 最近 2 轮原样保留，并且从一次用户发言开始。
	tail := r.Messages[1:]
	if !tail[0].IsUserInput() || tail[0].Text() != "问题ce" || len(tail) != 8 {
		t.Fatalf("应保留最近 2 轮（8 条）: %d 条, 第一条 %q", len(tail), tail[0].Text())
	}
	assertPaired(t, r.Messages)
	if r.AfterTokens >= r.BeforeTokens || r.BeforeTokens != before {
		t.Fatalf("token: %d → %d (估算 %d)", r.BeforeTokens, r.AfterTokens, before)
	}
	// 摘要请求带上了较早的内容，但不含被保留的最近一轮。
	if len(p.summaryReqs) != 1 {
		t.Fatalf("应调用一次摘要: %d", len(p.summaryReqs))
	}
	prompt := p.summaryReqs[0].Messages[0].Text()
	if !strings.Contains(prompt, "问题ca") || strings.Contains(prompt, "问题cf") {
		t.Fatalf("摘要输入应包含较早的轮、不含保留的轮")
	}
}

// 一轮里读了很多大文件：最近两轮本身就超出预算，只能从轮内的安全位置切，且仍不能拆开工具调用。
func TestCompactCutsInsideHugeTurnAtSafeBoundary(t *testing.T) {
	p := &stubProvider{summary: "S"}
	msgs := []provider.Message{txt(provider.RoleUser, "整理这个仓库")}
	for i := 0; i < 8; i++ {
		id := "t" + string(rune('a'+i))
		msgs = append(msgs, call(id, "read_file"), result(id, strings.Repeat("y", 8000)))
	}
	total := estimateMessages(msgs)
	c := newCompactor(p, total*10/9)

	r, err := c.Compact(context.Background(), CompactRequest{Messages: msgs})
	if err != nil || !r.Changed {
		t.Fatalf("应压缩: %v %+v", err, r)
	}
	assertPaired(t, r.Messages)
	if r.Messages[1].Role != provider.RoleAssistant || len(r.Messages[1].ToolCalls()) == 0 {
		t.Fatalf("保留部分应从一个完整的 assistant(tool_use) 开始: %+v", r.Messages[1])
	}
	if estimateMessages(r.Messages[1:]) > int(float64(c.Window)*DefaultCompactKeep) {
		t.Fatal("保留部分不应超过预算")
	}
}

func TestCompactNothingToCut(t *testing.T) {
	p := &stubProvider{summary: "S"}
	// 只有一条很大的用户发言：没有安全切点。
	msgs := []provider.Message{txt(provider.RoleUser, strings.Repeat("字", 5000))}
	r, err := newCompactor(p, 1000).Compact(context.Background(), CompactRequest{Messages: msgs})
	if err != nil || r.Changed || len(p.summaryReqs) != 0 {
		t.Fatalf("无处可切应保持原样: %v %+v", err, r)
	}
	// 只有"上一次的摘要 + 一轮"：被切掉的只是旧摘要，不重复摘要。
	msgs = []provider.Message{summaryMessage(strings.Repeat("旧", 900)), txt(provider.RoleUser, "接着做"), txt(provider.RoleAssistant, "好")}
	r, err = newCompactor(p, 1000).Compact(context.Background(), CompactRequest{Messages: msgs})
	if err != nil || r.Changed {
		t.Fatalf("只剩旧摘要可切时不应再压缩: %v %+v", err, r)
	}
}

func TestCompactMergesPreviousSummary(t *testing.T) {
	p := &stubProvider{summary: "合并后的摘要"}
	msgs := append([]provider.Message{summaryMessage("第一次的摘要")}, bigHistory(6, 4000)...)
	c := newCompactor(p, estimateMessages(msgs)*10/9)
	r, err := c.Compact(context.Background(), CompactRequest{Messages: msgs})
	if err != nil || !r.Changed {
		t.Fatal(err)
	}
	if !strings.Contains(p.summaryReqs[0].Messages[0].Text(), "第一次的摘要") {
		t.Fatal("旧摘要应作为输入交给模型合并")
	}
	summaries := 0
	for _, m := range r.Messages {
		if m.IsSummary() {
			summaries++
		}
	}
	if summaries != 1 {
		t.Fatalf("结果里只应有一条摘要: %d", summaries)
	}
}

func TestCompactFailureAndCancel(t *testing.T) {
	msgs := bigHistory(6, 4000)
	window := estimateMessages(msgs) * 10 / 9

	p := &stubProvider{summaryErr: errors.New("boom")}
	r, err := newCompactor(p, window).Compact(context.Background(), CompactRequest{Messages: msgs})
	if err == nil || r.Changed || len(r.Messages) != len(msgs) {
		t.Fatalf("失败应返回错误且历史不变: %v %+v", err, r.Changed)
	}

	p = &stubProvider{summary: "  \n"}
	if _, err = newCompactor(p, window).Compact(context.Background(), CompactRequest{Messages: msgs}); err == nil {
		t.Fatal("空摘要应视为失败")
	}

	// 摘要比原文还大：不替换。
	p = &stubProvider{summary: strings.Repeat("长", 100000)}
	r, err = newCompactor(p, window).Compact(context.Background(), CompactRequest{Messages: msgs})
	if err != nil || r.Changed {
		t.Fatalf("摘要更大时应保持原样: %v %v", err, r.Changed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p = &stubProvider{summaryHook: func(ctx context.Context) error { cancel(); return ctx.Err() }}
	if _, err = newCompactor(p, window).Compact(ctx, CompactRequest{Messages: msgs}); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应返回 context.Canceled: %v", err)
	}
}

// ---- 在 Agent 循环里 ----

type recorder struct {
	mu  sync.Mutex
	evs []event.Event
}

func (r *recorder) Publish(ev event.Event) event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evs = append(r.evs, ev)
	return ev
}

func runAgent(t *testing.T, p *stubProvider, window int, ctx context.Context) (*Session, *recorder, TurnResult) {
	t.Helper()
	sess := NewSession("s1", "stub", "m")
	for _, m := range bigHistory(6, 4000) {
		sess.appendMessage(m)
	}
	rec := &recorder{}
	a := NewAgent(AgentConfig{
		Provider: p, Model: "m", Publisher: rec, SessionID: "s1",
		Compactor: &SummaryCompactor{Provider: p, Model: "m", Window: window},
	})
	res := a.Run(ctx, sess, "继续")
	return sess, rec, res
}

func eventsOf[T any](rec *recorder) []T {
	var out []T
	for _, ev := range rec.evs {
		if v, ok := ev.Data.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func TestAgentCompactsBeforeModelCall(t *testing.T) {
	p := &stubProvider{summary: "摘要正文"}
	window := estimateMessages(bigHistory(6, 4000)) * 10 / 9
	sess, rec, res := runAgent(t, p, window, context.Background())
	if res.Reason != event.TurnCompleted {
		t.Fatalf("%+v", res)
	}
	ce := eventsOf[event.ContextCompacted](rec)
	if len(ce) != 1 || ce[0].Summary != "摘要正文" || ce[0].RemovedMessages <= 0 || ce[0].AfterTokens >= ce[0].BeforeTokens {
		t.Fatalf("context_compacted: %+v", ce)
	}
	// 发给聊天模型的请求用的是压缩后的历史，并且仍然配对完整。
	if len(p.chatReqs) != 1 || !p.chatReqs[0].Messages[0].IsSummary() {
		t.Fatalf("聊天请求应以摘要开头: %+v", p.chatReqs)
	}
	assertPaired(t, p.chatReqs[0].Messages)
	// 会话里的历史被替换（持久化的就是它），并使分页游标失效。
	snap := sess.Snapshot()
	if !snap.Messages[0].IsSummary() || snap.epoch == 0 {
		t.Fatalf("历史应被替换并增加 epoch: epoch=%d", snap.epoch)
	}
	// 回放：摘要显示成"上下文已压缩"，不是一条用户发言。
	var sawCompacted bool
	for _, ev := range ReplayEvents(sess, nil) {
		if c, ok := ev.Data.(event.ContextCompacted); ok && c.Replay && c.Summary == "摘要正文" {
			sawCompacted = true
		}
		if u, ok := ev.Data.(event.UserMessage); ok && strings.Contains(u.Text, provider.SummaryHeader[:12]) {
			t.Fatal("摘要不应回放成用户发言")
		}
	}
	if !sawCompacted {
		t.Fatal("回放里应有 context_compacted")
	}
}

func TestAgentContinuesWhenCompactionFails(t *testing.T) {
	p := &stubProvider{summaryErr: errors.New("摘要模型挂了")}
	window := estimateMessages(bigHistory(6, 4000)) * 10 / 9
	sess, rec, res := runAgent(t, p, window, context.Background())
	if res.Reason != event.TurnCompleted {
		t.Fatalf("摘要失败不应终止本轮: %+v", res)
	}
	errs := eventsOf[event.Error](rec)
	if len(errs) != 1 || errs[0].Code != "compact_failed" || errs[0].Fatal {
		t.Fatalf("应发一条非致命的 compact_failed: %+v", errs)
	}
	if len(sess.Snapshot().Messages) != 24+2 || len(eventsOf[event.ContextCompacted](rec)) != 0 {
		t.Fatal("失败时历史应保持原样")
	}
}

func TestAgentCancelDuringCompaction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &stubProvider{summaryHook: func(c context.Context) error { cancel(); return c.Err() }}
	window := estimateMessages(bigHistory(6, 4000)) * 10 / 9
	sess, rec, res := runAgent(t, p, window, ctx)
	if res.Reason != event.TurnCancelled {
		t.Fatalf("压缩时被取消应以 cancelled 结束: %+v", res)
	}
	if len(p.chatReqs) != 0 || len(eventsOf[event.Error](rec)) != 0 {
		t.Fatal("取消后不应再请求模型，也不应报错")
	}
	if len(sess.Snapshot().Messages) != 24+1 {
		t.Fatal("取消时历史保持原样（只多了本轮的用户发言）")
	}
}
