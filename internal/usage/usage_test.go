package usage

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCost(t *testing.T) {
	// 输入口径含缓存：1000 输入里 400 是缓存读、100 是缓存写 → 普通输入 500。
	p := Price{In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75}
	r := Row{Input: 1000, Output: 200, CacheRead: 400, CacheWrite: 100}
	want := (500*3 + 400*0.3 + 100*3.75 + 200*15) / 1e6
	if got := Cost(p, r); !near(got, want) {
		t.Fatalf("cost %v want %v", got, want)
	}
	// 缓存数据异常（缓存 > 输入）时普通输入不能为负。
	if got := Cost(p, Row{Input: 10, CacheRead: 100}); got < 0 {
		t.Fatalf("不应出现负的费用: %v", got)
	}
}

func prices(profile, model string) PriceInfo {
	switch profile + "/" + model {
	case "anthropic/sonnet":
		return PriceInfo{Known: true, Price: Price{In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75}}
	case "chatgpt/gpt":
		return PriceInfo{Plan: true}
	}
	return PriceInfo{}
}

func TestBuildTreeAndPricing(t *testing.T) {
	rows := []Row{
		{Day: "2026-10-25", Profile: "anthropic", Model: "sonnet", Requests: 2, Input: 2000, Output: 200, CacheRead: 1000},
		{Day: "2026-10-25", Profile: "deepseek", Model: "v4", Requests: 3, FailedTries: 1, HandedOn: 1, Input: 5000, Output: 500},
		{Day: "2026-10-25", Profile: "chatgpt", Model: "gpt", Requests: 4, Input: 100, Output: 10},
		{Day: "2026-10-24", Profile: "anthropic", Model: "sonnet", Requests: 1, Input: 1000, Output: 100},
	}
	rep := Build(rows, ByDay, prices)
	if len(rep.Nodes) != 2 || rep.Nodes[0].Key != "2026-10-25" {
		t.Fatalf("按天分组，顺序沿用输入: %+v", rep.Nodes)
	}
	day := rep.Nodes[0]
	if len(day.Children) != 3 || day.Children[0].Key != "deepseek/v4" {
		t.Fatalf("同一天内用得多的排前面: %+v", day.Children)
	}
	// 没有价格的不计入花费也不当作免费；套餐不计金额也不算"没有价格"。
	if day.UnpricedCalls != 3 || day.PlanCalls != 4 || day.Requests != 9 {
		t.Fatalf("day: %+v", day.Totals)
	}
	wantDay := Cost(Price{In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75}, rows[0])
	if !near(day.CostUSD, wantDay) {
		t.Fatalf("day cost %v want %v", day.CostUSD, wantDay)
	}
	if !near(rep.Totals.CostUSD, wantDay+Cost(Price{In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75}, rows[3])) {
		t.Fatalf("total cost %v", rep.Totals.CostUSD)
	}
	if rep.Totals.FailedTries != 1 || rep.Totals.HandedOn != 1 || rep.Totals.Input != 8100 {
		t.Fatalf("totals: %+v", rep.Totals)
	}
	if len(rep.UnpricedModels) != 1 || rep.UnpricedModels[0] != "deepseek/v4" {
		t.Fatalf("应列出没有价格的模型: %v", rep.UnpricedModels)
	}
	var plan Node
	for _, c := range day.Children {
		if c.Key == "chatgpt/gpt" {
			plan = c
		}
	}
	if !plan.Plan || plan.Priced {
		t.Fatalf("套餐叶子节点: %+v", plan)
	}
	if !near(day.CacheHit, 1000.0/7100) {
		t.Fatalf("缓存命中率: %v", day.CacheHit)
	}
}

func TestBuildOtherGroupings(t *testing.T) {
	rows := []Row{
		{Profile: "anthropic", Model: "sonnet", Requests: 1, Input: 100},
		{Profile: "anthropic", Model: "opus", Requests: 1, Input: 900},
		{Profile: "deepseek", Model: "v4", Requests: 1, Input: 500},
	}
	if rep := Build(rows, ByModel, prices); len(rep.Nodes) != 3 || rep.Nodes[0].Key != "anthropic/opus" || len(rep.Nodes[0].Children) != 0 {
		t.Fatalf("按模型：扁平，用得多的在前: %+v", rep.Nodes)
	}
	rep := Build(rows, ByProfile, prices)
	if len(rep.Nodes) != 2 || rep.Nodes[0].Key != "anthropic" || len(rep.Nodes[0].Children) != 2 || rep.Nodes[0].Input != 1000 {
		t.Fatalf("按厂商: %+v", rep.Nodes)
	}
	s := Build([]Row{{SessionID: "s1", SessionTitle: "标题", Profile: "a", Model: "m"}, {SessionID: "", Profile: "a", Model: "m"}, {SessionID: "s2", Profile: "a", Model: "m"}}, BySession, prices)
	if s.Nodes[0].Label != "标题" || s.Nodes[1].Label != "（未关联会话）" || s.Nodes[2].Label != "（无标题）" {
		t.Fatalf("会话标签: %+v", s.Nodes)
	}
	if empty := Build(nil, ByDay, prices); empty.Nodes == nil || empty.UnpricedModels == nil || empty.Totals.Requests != 0 {
		t.Fatal("空报表也要有非 nil 的切片（序列化成 [] 而不是 null）")
	}
}

func TestSplitKey(t *testing.T) {
	if p, m := SplitKey("openrouter/anthropic/claude"); p != "openrouter" || m != "anthropic/claude" {
		t.Fatalf("%q %q", p, m)
	}
}

func TestCSVEscapesAndNeutralizesFormulas(t *testing.T) {
	var b bytes.Buffer
	err := WriteCSV(&b, []Event{{Day: "2026-10-25", UsageEvent: provider.UsageEvent{
		Time: time.Date(2026, 10, 25, 1, 2, 3, 0, time.UTC), Profile: "=cmd|' /C calc'!A0", Model: `m,"x"`, Status: "ok", TryNo: 1,
		Usage: provider.Usage{InputTokens: 5},
	}}})
	out := b.String()
	if err != nil || !strings.Contains(out, "'=cmd") || strings.Contains(out, ",=cmd") || !strings.Contains(out, `"m,""x"""`) || !strings.Contains(out, "2026-10-25T01:02:03Z") {
		t.Fatalf("%v\n%s", err, out)
	}
}

type memSink struct {
	mu  sync.Mutex
	got []provider.UsageEvent
	err error
}

func (m *memSink) AppendUsage(ev provider.UsageEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.got = append(m.got, ev)
	return m.err
}

func TestWriterFlushesAndNeverBlocks(t *testing.T) {
	s := &memSink{}
	w := NewWriter(s, nil)
	for i := 0; i < 100; i++ {
		w.RecordUsage(provider.UsageEvent{TryNo: i})
	}
	w.Close()
	if len(s.got) != 100 || w.Dropped() != 0 {
		t.Fatalf("Close 应等写完: %d 丢弃 %d", len(s.got), w.Dropped())
	}
	w.RecordUsage(provider.UsageEvent{}) // 关闭之后不能 panic

	// 写入端很慢时，队列满了丢弃而不是阻塞调用方。
	block := make(chan struct{})
	slow := NewWriter(blockingSink{block}, nil)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 2000; i++ {
			slow.RecordUsage(provider.UsageEvent{})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RecordUsage 不应阻塞")
	}
	if slow.Dropped() == 0 {
		t.Fatal("应当有丢弃计数")
	}
	close(block)
	slow.Close()

	// 写入失败通过回调报告，不影响后续。
	var errs int
	bad := NewWriter(&memSink{err: errors.New("disk full")}, func(error) { errs++ })
	bad.RecordUsage(provider.UsageEvent{})
	bad.Close()
	if errs != 1 {
		t.Fatalf("errs=%d", errs)
	}
}

type blockingSink struct{ c chan struct{} }

func (b blockingSink) AppendUsage(provider.UsageEvent) error { <-b.c; return nil }
