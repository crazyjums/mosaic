package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// 测试用的厂商：按脚本依次返回结果，并记录被调用的次数和收到的历史。
type step struct {
	err    error
	deltas []Delta // 先发出这些增量
	block  bool    // 一直阻塞到 ctx 结束（模拟首字节超时）
	usage  *Usage  // 成功时附带的用量
}

type stub struct {
	name  string
	mu    sync.Mutex
	steps []step
	calls int
	seen  [][]Message
}

func (s *stub) Name() string { return s.name }
func (s *stub) Stream(ctx context.Context, req Request, on func(Delta)) error {
	s.mu.Lock()
	var st step
	if s.calls < len(s.steps) {
		st = s.steps[s.calls]
	} else if len(s.steps) > 0 {
		st = s.steps[len(s.steps)-1]
	}
	s.calls++
	s.seen = append(s.seen, req.Messages)
	s.mu.Unlock()
	if st.block {
		<-ctx.Done()
		return ctx.Err()
	}
	for _, d := range st.deltas {
		on(d)
	}
	if st.err != nil {
		return st.err
	}
	on(Delta{Type: DeltaText, Text: "来自 " + s.name})
	if st.usage != nil {
		on(Delta{Type: DeltaUsage, Usage: st.usage})
	}
	on(Delta{Type: DeltaStop, StopReason: StopEndTurn})
	return nil
}
func (s *stub) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }

func httpErr(status int, msg string) error {
	return &HTTPError{Status: status, Err: errors.New(msg)}
}

type recorder struct {
	mu sync.Mutex
	ev []UsageEvent
}

func (r *recorder) RecordUsage(e UsageEvent) { r.mu.Lock(); r.ev = append(r.ev, e); r.mu.Unlock() }
func (r *recorder) events() []UsageEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]UsageEvent(nil), r.ev...)
}

// newGroup 用若干 stub 搭一个模型组，时钟可控。
func newGroup(t *testing.T, cfg GroupConfig, stubs ...*stub) (*Group, *recorder, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	h := NewHealth()
	h.now = func() time.Time { return clock }
	rec := &recorder{}
	cfg.Name = "主力"
	for _, s := range stubs {
		cfg.Members = append(cfg.Members, GroupMember{Profile: s.name, Model: "m"})
	}
	g := &Group{cfg: cfg, health: h, rec: rec}
	for _, s := range stubs {
		g.members = append(g.members, &groupMember{GroupMember: GroupMember{Profile: s.name, Model: "m"}, key: MemberKey(s.name, "m"), leaf: s})
	}
	return g, rec, &clock
}

func run(g *Group, ctx context.Context) (text string, route *Route, err error) {
	var b strings.Builder
	err = g.Stream(ctx, Request{Model: GroupModel, Messages: []Message{UserText("hi")}}, func(d Delta) {
		switch d.Type {
		case DeltaText:
			b.WriteString(d.Text)
		case DeltaRoute:
			route = d.Route
		}
	})
	return b.String(), route, err
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want ErrorKind
	}{
		{httpErr(429, "rate limited"), KindRateLimit},
		{httpErr(429, "You exceeded your current quota"), KindQuota},
		{httpErr(402, "pay"), KindQuota},
		{httpErr(401, "bad key"), KindAuth},
		{httpErr(403, "forbidden"), KindAuth},
		{httpErr(500, "boom"), KindServer},
		{httpErr(529, "overloaded"), KindServer},
		{httpErr(400, "This model's maximum context length is 8192 tokens"), KindContext},
		{httpErr(400, "prompt is too long: 250000 tokens"), KindContext},
		{httpErr(413, "too big"), KindContext},
		{httpErr(400, "invalid tool schema"), KindOther},
		{httpErr(404, "no such model"), KindOther},
		{NewQuotaError("ChatGPT 套餐额度已用完"), KindQuota},
		{fmt.Errorf("x: %w", ErrStreamInterrupted), KindNetwork},
		{fmt.Errorf("x: %w", ErrFirstByteTimeout), KindNetwork},
		{context.Canceled, KindCancelled},
		{errors.New("some adapter bug"), KindOther},
		{nil, KindNone},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("Classify(%v) = %q，期望 %q", c.err, got, c.want)
		}
	}
	for _, k := range []ErrorKind{KindOther, KindCancelled, KindNone} {
		if k.Failover() {
			t.Errorf("%q 不应触发转移", k)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	if ParseRetryAfter("30") != 30*time.Second || ParseRetryAfter("") != 0 || ParseRetryAfter("soon") != 0 {
		t.Fatal("秒数 / 空 / 非法")
	}
	if d := ParseRetryAfter(time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)); d < 80*time.Second || d > 91*time.Second {
		t.Fatalf("HTTP 日期: %v", d)
	}
}

func TestGroupFailsOverBeforeAnyOutput(t *testing.T) {
	a := &stub{name: "A", steps: []step{{err: httpErr(429, "slow down")}}}
	b := &stub{name: "B", steps: []step{{usage: &Usage{InputTokens: 10, OutputTokens: 2}}}}
	g, rec, _ := newGroup(t, GroupConfig{}, a, b)

	text, route, err := run(g, WithCall(context.Background(), CallInfo{SessionID: "s1"}))
	if err != nil || text != "来自 B" {
		t.Fatalf("应由 B 应答: %q %v", text, err)
	}
	if route == nil || route.Profile != "B" || route.Try != 2 || len(route.HandedOn) != 1 || route.HandedOn[0].Profile != "A" || route.HandedOn[0].Kind != KindRateLimit {
		t.Fatalf("route: %+v", route)
	}
	ev := rec.events()
	if len(ev) != 2 || ev[0].Profile != "A" || ev[0].Status != StatusHandedOn || ev[0].ErrorKind != KindRateLimit || ev[0].TryNo != 1 ||
		ev[1].Profile != "B" || ev[1].Status != StatusOK || ev[1].TryNo != 2 || ev[1].Usage.InputTokens != 10 || ev[1].Group != "主力" || ev[1].SessionID != "s1" {
		t.Fatalf("账本: %+v", ev)
	}
	if st := g.health.Status("A", "m"); st.State != StateCooling || st.Kind != KindRateLimit {
		t.Fatalf("A 应进入冷却: %+v", st)
	}
}

func TestGroupDoesNotFailOverOnClientErrors(t *testing.T) {
	a := &stub{name: "A", steps: []step{{err: httpErr(400, "invalid tool schema")}}}
	b := &stub{name: "B"}
	g, rec, _ := newGroup(t, GroupConfig{}, a, b)
	if _, _, err := run(g, context.Background()); err == nil || b.count() != 0 {
		t.Fatalf("请求本身的问题不应换成员: %v b=%d", err, b.count())
	}
	if st := g.health.Status("A", "m"); st.State != StateHealthy {
		t.Fatalf("不是成员的错，不应冷却: %+v", st)
	}
	if ev := rec.events(); len(ev) != 1 || ev[0].Status != StatusError || ev[0].ErrorKind != KindOther {
		t.Fatalf("账本: %+v", ev)
	}
}

func TestGroupDoesNotFailOverAfterOutput(t *testing.T) {
	a := &stub{name: "A", steps: []step{{deltas: []Delta{{Type: DeltaText, Text: "半截"}}, err: fmt.Errorf("x: %w", ErrStreamInterrupted)}}}
	b := &stub{name: "B"}
	g, _, _ := newGroup(t, GroupConfig{}, a, b)
	text, _, err := run(g, context.Background())
	if err == nil || b.count() != 0 || text != "半截" {
		t.Fatalf("已输出内容后不应自动切换: %q %v b=%d", text, err, b.count())
	}
}

func TestGroupCooldownAndProbe(t *testing.T) {
	a := &stub{name: "A", steps: []step{{err: httpErr(503, "down")}, {}}}
	b := &stub{name: "B"}
	g, _, clock := newGroup(t, GroupConfig{}, a, b)

	run(g, context.Background()) // A 失败 → B
	run(g, context.Background()) // A 在冷却，直接 B
	if a.count() != 1 || b.count() != 2 {
		t.Fatalf("冷却期间不应再试 A: a=%d b=%d", a.count(), b.count())
	}
	*clock = clock.Add(31 * time.Second) // 冷却 30 秒已过
	text, _, _ := run(g, context.Background())
	if a.count() != 2 || text != "来自 A" {
		t.Fatalf("冷却到期后应试探 A: a=%d %q", a.count(), text)
	}
	if st := g.health.Status("A", "m"); st.State != StateHealthy {
		t.Fatalf("试探成功后应恢复健康: %+v", st)
	}
}

func TestCooldownBackoffDoublesAndCaps(t *testing.T) {
	h := NewHealth()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return clock }
	var waits []time.Duration
	for i := 0; i < 8; i++ {
		h.Fail("A/m", KindServer, 0, "x")
		waits = append(waits, time.UnixMilli(h.Status("A", "m").Until).Sub(clock))
	}
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("第 %d 次失败的冷却 %v，期望 %v", i+1, waits[i], want[i])
		}
	}
	h2 := NewHealth()
	h2.now = h.now
	h2.Fail("B/m", KindRateLimit, 0, "x")
	if d := time.UnixMilli(h2.Status("B", "m").Until).Sub(clock); d != time.Minute {
		t.Fatalf("限流默认 60 秒: %v", d)
	}
	h2.Fail("C/m", KindRateLimit, 5*time.Minute, "x")
	if d := time.UnixMilli(h2.Status("C", "m").Until).Sub(clock); d != 5*time.Minute {
		t.Fatalf("应遵循 Retry-After: %v", d)
	}
	h2.Fail("D/m", KindAuth, 0, "bad key")
	if h2.Status("D", "m").State != StateDisabled || h2.acquire("D/m") {
		t.Fatal("认证失败应停用")
	}
	h2.ResetProfile("D")
	if h2.Status("D", "m").State != StateHealthy {
		t.Fatal("重置配置档后应恢复")
	}
	h2.Fail("E/m", KindContext, 0, "x")
	if h2.Status("E", "m").State != StateHealthy {
		t.Fatal("上下文超长不是成员的问题，不应冷却")
	}
}

func TestOnlyOneProbeAtATime(t *testing.T) {
	h := NewHealth()
	clock := time.Now()
	h.now = func() time.Time { return clock }
	h.Fail("A/m", KindServer, 0, "x")
	clock = clock.Add(time.Minute)
	if !h.acquire("A/m") || h.acquire("A/m") {
		t.Fatal("冷却到期后只放行一个试探请求")
	}
	h.release("A/m")
	if !h.acquire("A/m") {
		t.Fatal("释放后可以再试探")
	}
}

func TestGroupAllFail(t *testing.T) {
	a := &stub{name: "A", steps: []step{{err: httpErr(500, "a down")}}}
	b := &stub{name: "B", steps: []step{{err: httpErr(429, "b busy")}}}
	g, rec, _ := newGroup(t, GroupConfig{}, a, b)
	_, _, err := run(g, context.Background())
	var af *AllFailedError
	if !errors.As(err, &af) || len(af.Attempts) != 2 || !strings.Contains(err.Error(), "a down") || !strings.Contains(err.Error(), "b busy") {
		t.Fatalf("应列出每个成员的失败原因: %v", err)
	}
	ev := rec.events()
	if len(ev) != 2 || ev[0].Status != StatusHandedOn || ev[1].Status != StatusError {
		t.Fatalf("最后一次不应标为已转交: %+v", ev)
	}
	// 全员冷却时仍然放行一次试探，而不是直接拒绝。
	a.steps, b.steps = []step{{}}, []step{{}}
	text, _, err := run(g, context.Background())
	if err != nil || text == "" {
		t.Fatalf("全员冷却时应强制试探冷却最早到期的成员: %q %v", text, err)
	}
}

func TestGroupSkipsDisabledAndReportsWhy(t *testing.T) {
	a := &stub{name: "A", steps: []step{{err: httpErr(401, "invalid api key")}}}
	g, _, _ := newGroup(t, GroupConfig{}, a)
	run(g, context.Background())
	_, _, err := run(g, context.Background())
	if err == nil || !strings.Contains(err.Error(), "已停用") || a.count() != 1 {
		t.Fatalf("认证失败的成员不应再被请求，并说明原因: %v a=%d", err, a.count())
	}
}

func TestGroupContextTooLongPicksLargerWindow(t *testing.T) {
	a := &stub{name: "A", steps: []step{{err: httpErr(400, "maximum context length is 8192 tokens")}}}
	small := &stub{name: "S"}
	big := &stub{name: "B"}
	g, _, _ := newGroup(t, GroupConfig{}, a, small, big)
	g.members[0].ctxWindow, g.members[1].ctxWindow, g.members[2].ctxWindow = 8000, 4000, 200000
	text, route, err := run(g, context.Background())
	if err != nil || text != "来自 B" || small.count() != 0 || route.Try != 2 {
		t.Fatalf("应跳过窗口更小的成员: %q %v small=%d", text, err, small.count())
	}
	if st := g.health.Status("A", "m"); st.State != StateHealthy {
		t.Fatalf("上下文超长不应让成员进入冷却: %+v", st)
	}
}

func TestGroupFirstByteTimeout(t *testing.T) {
	a := &stub{name: "A", steps: []step{{block: true}}}
	b := &stub{name: "B"}
	g, rec, _ := newGroup(t, GroupConfig{}, a, b)
	g.firstByte = 50 * time.Millisecond
	text, route, err := run(g, context.Background())
	if err != nil || text != "来自 B" || route.HandedOn[0].Kind != KindNetwork {
		t.Fatalf("首字节超时应转交: %q %v %+v", text, err, route)
	}
	if ev := rec.events(); ev[0].Status != StatusHandedOn || ev[0].ErrorKind != KindNetwork {
		t.Fatalf("账本: %+v", ev[0])
	}
}

func TestGroupUserCancelDoesNotPenalize(t *testing.T) {
	a := &stub{name: "A", steps: []step{{block: true}}}
	b := &stub{name: "B"}
	g, rec, _ := newGroup(t, GroupConfig{}, a, b)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, _, err := run(g, ctx)
	if err == nil || b.count() != 0 {
		t.Fatalf("用户取消不应转交: %v b=%d", err, b.count())
	}
	if st := g.health.Status("A", "m"); st.State != StateHealthy {
		t.Fatalf("取消不是成员的错: %+v", st)
	}
	if ev := rec.events(); len(ev) != 1 || ev[0].Status != StatusCancelled {
		t.Fatalf("账本: %+v", ev)
	}
}

func TestGroupStickyAndRotate(t *testing.T) {
	a := &stub{name: "A", steps: []step{{err: httpErr(500, "x")}, {}}}
	b := &stub{name: "B"}
	g, _, clock := newGroup(t, GroupConfig{Sticky: true}, a, b)
	ctx := WithCall(context.Background(), CallInfo{SessionID: "s1"})
	run(g, ctx) // A 失败 → B，会话 s1 粘在 B
	*clock = clock.Add(time.Hour)
	text, _, _ := run(g, ctx)
	if text != "来自 B" {
		t.Fatalf("同一会话应继续用 B（即使 A 已恢复）: %q", text)
	}
	if text, _, _ = run(g, WithCall(context.Background(), CallInfo{SessionID: "s2"})); text != "来自 A" {
		t.Fatalf("新会话按顺序从 A 开始: %q", text)
	}

	c := &stub{name: "C"}
	d := &stub{name: "D"}
	r, _, _ := newGroup(t, GroupConfig{Strategy: StrategyRotate, Sticky: true}, c, d)
	var got []string
	for _, sid := range []string{"1", "2", "3", "1"} {
		text, _, _ := run(r, WithCall(context.Background(), CallInfo{SessionID: sid}))
		got = append(got, text)
	}
	if strings.Join(got, ",") != "来自 C,来自 D,来自 C,来自 C" {
		t.Fatalf("rotate：新会话轮换，已有会话保持: %v", got)
	}
}

func TestGroupLeastUsed(t *testing.T) {
	a := &stub{name: "A"}
	b := &stub{name: "B"}
	g, _, _ := newGroup(t, GroupConfig{Strategy: StrategyLeastUsed}, a, b)
	g.todayTokens = func(p string) int64 {
		if p == "A" {
			return 1000
		}
		return 10
	}
	if text, _, _ := run(g, context.Background()); text != "来自 B" {
		t.Fatalf("应优先今天用得最少的: %q", text)
	}
}

func TestGroupStripsThinkingWhenMemberChanges(t *testing.T) {
	a := &stub{name: "A", steps: []step{{err: httpErr(500, "x")}}}
	b := &stub{name: "B"}
	g, _, _ := newGroup(t, GroupConfig{}, a, b)
	hist := []Message{UserText("q"), {Role: RoleAssistant, Content: []Block{{Type: BlockThinking, Text: "想", Signature: "sig"}, {Type: BlockText, Text: "答"}}}, UserText("q2")}
	g.Stream(context.Background(), Request{Messages: hist}, func(Delta) {})
	// B 不是上一个应答的成员，历史里的思考块（签名只对原模型有效）必须去掉。
	for _, m := range b.seen[0] {
		for _, blk := range m.Content {
			if blk.Type == BlockThinking {
				t.Fatal("换成员时应去掉思考块")
			}
		}
	}
	if len(b.seen[0]) != 3 || b.seen[0][1].Content[0].Text != "答" {
		t.Fatalf("其余内容应保留: %+v", b.seen[0])
	}
	// 同一个成员连续应答：保留思考块（Anthropic 要求工具循环里带回签名）。
	g.lastServed = MemberKey("B", "m")
	g.health.Reset("A/m")
	a.steps = []step{{}}
	g.lastServed = MemberKey("A", "m")
	g.Stream(context.Background(), Request{Messages: hist}, func(Delta) {})
	if got := a.seen[len(a.seen)-1][1].Content[0].Type; got != BlockThinking {
		t.Fatalf("同一成员应保留思考块: %v", got)
	}
}

func TestGroupConfigValidation(t *testing.T) {
	ok := GroupConfig{Name: "g", Members: []GroupMember{{"a", "m"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]GroupConfig{
		"无名称":  {Members: ok.Members},
		"无成员":  {Name: "g"},
		"空模型":  {Name: "g", Members: []GroupMember{{"a", ""}}},
		"重复":   {Name: "g", Members: []GroupMember{{"a", "m"}, {"a", "m"}}},
		"坏策略":  {Name: "g", Members: ok.Members, Strategy: "random"},
		"超时过大": {Name: "g", Members: ok.Members, FirstByteTimeoutS: 9999},
	} {
		if c.Validate() == nil {
			t.Errorf("%s 应校验失败", name)
		}
	}
}

func TestCatalogBuildsGroupsAndMeters(t *testing.T) {
	reg := NewRegistry()
	reg.Register("stub", func(cfg ProfileConfig) (Provider, error) {
		if cfg.Name == "broken" {
			return nil, errors.New("缺少密钥")
		}
		return &stub{name: cfg.Name}, nil
	})
	rec := &recorder{}
	cfgs := []ProfileConfig{
		{Name: "a", Type: "stub", Models: []string{"m"}, Options: map[string]any{"models_meta": map[string]any{"m": map[string]any{"context_window": 1000}}}},
		{Name: "broken", Type: "stub", Models: []string{"m"}},
		{Name: "g", Type: GroupType, Models: []string{GroupModel}, DefaultModel: GroupModel, Options: GroupOptions(GroupConfig{
			Members: []GroupMember{{"broken", "m"}, {"a", "m"}, {"gone", "m"}}, Strategy: StrategyOrder, Sticky: true, FirstByteTimeoutS: 20})},
		{Name: "empty", Type: GroupType, Models: []string{GroupModel}, Options: GroupOptions(GroupConfig{Members: []GroupMember{{"broken", "m"}}})},
		{Name: "nested", Type: GroupType, Models: []string{GroupModel}, Options: GroupOptions(GroupConfig{Members: []GroupMember{{"g", GroupModel}, {"a", "m"}}})},
	}
	c := NewCatalogWith(reg, cfgs, CatalogOptions{Recorder: rec})

	p, _, err := c.Get("g")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Get("empty"); err == nil || !strings.Contains(err.Error(), "没有可用的成员") {
		t.Fatalf("没有可用成员的组应不可用: %v", err)
	}
	if _, _, err := c.Get("nested"); err != nil {
		t.Fatalf("嵌套的成员被标为不可用，但组里还有别的成员: %v", err)
	}
	if err := p.Stream(context.Background(), Request{Model: GroupModel}, func(Delta) {}); err != nil {
		t.Fatal(err)
	}
	// 通过模型组调用：用量记在成员 a 上，并带上组名。
	if ev := rec.events(); len(ev) != 1 || ev[0].Profile != "a" || ev[0].Group != "g" || ev[0].Model != "m" {
		t.Fatalf("账本: %+v", ev)
	}
	// 直连：同样会被计量。
	direct, _, _ := c.Get("a")
	direct.Stream(context.Background(), Request{Model: "m"}, func(Delta) {})
	if ev := rec.events(); len(ev) != 2 || ev[1].Profile != "a" || ev[1].Group != "" {
		t.Fatalf("直连也要记账: %+v", ev)
	}
	sts := c.GroupStatuses()
	if len(sts) != 3 || sts[0].Name != "g" || len(sts[0].Members) != 3 {
		t.Fatalf("状态: %+v", sts)
	}
	var unavailable []string
	for _, m := range sts[0].Members {
		if m.Unavailable != "" {
			unavailable = append(unavailable, m.Profile)
		}
	}
	if strings.Join(unavailable, ",") != "broken,gone" {
		t.Fatalf("不可用成员应带原因: %+v", sts[0].Members)
	}
	if infos := c.Profiles(); infos[2].Type != GroupType || infos[2].Models[0] != GroupModel {
		t.Fatalf("profiles: %+v", infos)
	}
}

func TestMeteredRecordsTTFTAndUsage(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	clock := time.Date(2026, 10, 25, 23, 59, 59, 0, time.UTC)
	now = func() time.Time { return clock }
	inner := &advancing{clock: &clock}
	rec := &recorder{}
	m := &metered{inner: inner, rec: rec, profile: "p"}
	ctx := WithCall(context.Background(), CallInfo{SessionID: "s", Source: SourceTitle})
	if err := m.Stream(ctx, Request{Model: "x"}, func(Delta) {}); err != nil {
		t.Fatal(err)
	}
	ev := rec.events()[0]
	if ev.TTFTMS != 300 || ev.DurationMS != 800 || ev.Usage.OutputTokens != 7 || ev.Source != SourceTitle || ev.SessionID != "s" || ev.Status != StatusOK || !ev.Time.Equal(time.Date(2026, 10, 25, 23, 59, 59, 0, time.UTC)) {
		t.Fatalf("%+v", ev)
	}
}

// advancing 在发出首个增量前把时钟拨快 300ms，结束时再拨 500ms。
type advancing struct{ clock *time.Time }

func (a *advancing) Name() string { return "adv" }
func (a *advancing) Stream(_ context.Context, _ Request, on func(Delta)) error {
	*a.clock = a.clock.Add(300 * time.Millisecond)
	on(Delta{Type: DeltaText, Text: "x"})
	*a.clock = a.clock.Add(500 * time.Millisecond)
	on(Delta{Type: DeltaUsage, Usage: &Usage{InputTokens: 1, OutputTokens: 7}})
	return nil
}
