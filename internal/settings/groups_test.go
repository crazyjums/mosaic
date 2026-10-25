package settings

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/store"
	"github.com/crazyjums/myllmapp/internal/usage"
)

// chatServer 是最小的 OpenAI 兼容聊天服务：status 非 200 时直接返回该状态码，否则回复 reply。
func chatServer(t *testing.T, status int, reply string, calls *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if status != 200 {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":{"message":"busy"}}`)
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\""+reply+"\"},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func addOpenAI(t *testing.T, m *Manager, name, url string, meta map[string]any) {
	t.Helper()
	opts := map[string]any{}
	if meta != nil {
		opts["models_meta"] = meta
	}
	if err := m.Save(ProviderInput{Name: name, Type: "openai", BaseURL: url, APIKey: "sk-" + name, Models: []string{"m"}, Options: opts}); err != nil {
		t.Fatal(err)
	}
}

func TestGroupCRUDAndValidation(t *testing.T) {
	var n atomic.Int32
	a, b := chatServer(t, 200, "A", &n), chatServer(t, 200, "B", &n)
	m, _, path := newEnv(t)
	addOpenAI(t, m, "a", a.URL, nil)
	addOpenAI(t, m, "b", b.URL, nil)

	good := GroupInput{GroupConfig: provider.GroupConfig{Name: "主力", Members: []provider.GroupMember{{Profile: "a", Model: "m"}, {Profile: "b", Model: "m"}}, Sticky: true, FirstByteTimeoutS: 20}}
	for name, in := range map[string]GroupInput{
		"成员配置档不存在": {GroupConfig: provider.GroupConfig{Name: "g", Members: []provider.GroupMember{{Profile: "nope", Model: "m"}}}},
		"模型不在列表里":  {GroupConfig: provider.GroupConfig{Name: "g", Members: []provider.GroupMember{{Profile: "a", Model: "zzz"}}}},
		"和厂商重名":    {GroupConfig: provider.GroupConfig{Name: "a", Members: good.Members}},
		"没有成员":     {GroupConfig: provider.GroupConfig{Name: "g"}},
	} {
		if err := m.SaveGroup(in); err == nil {
			t.Errorf("%s 应失败", name)
		}
	}
	if err := m.SaveGroup(good); err != nil {
		t.Fatal(err)
	}
	// 嵌套、与其他模型组重名。
	if err := m.SaveGroup(GroupInput{GroupConfig: provider.GroupConfig{Name: "套娃", Members: []provider.GroupMember{{Profile: "主力", Model: provider.GroupModel}}}}); err == nil {
		t.Fatal("模型组不能嵌套")
	}
	if err := m.SaveGroup(good); err == nil {
		t.Fatal("新建同名模型组应失败")
	}

	gs := m.Groups()
	if len(gs) != 1 || gs[0].Name != "主力" || !gs[0].Available || gs[0].Strategy != provider.StrategyOrder {
		t.Fatalf("groups: %+v", gs)
	}
	// 模型组和厂商一起出现在配置档列表（下拉框）里，模型名固定为 auto，不能在界面上当普通厂商删除。
	var found bool
	for _, p := range m.Providers() {
		if p.Name == "主力" {
			found = p.Type == provider.GroupType && len(p.Models) == 1 && p.Models[0] == provider.GroupModel && p.Available
		}
	}
	if !found {
		t.Fatalf("providers: %+v", m.Providers())
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), `"groups"`) {
		t.Fatal("模型组应写入设置文件")
	}

	// 改名；编辑时名字不算冲突。
	edit := good
	edit.OriginalName = "主力"
	edit.Name = "备用组"
	if err := m.SaveGroup(edit); err != nil {
		t.Fatal(err)
	}
	if gs := m.Groups(); len(gs) != 1 || gs[0].Name != "备用组" {
		t.Fatalf("改名后: %+v", gs)
	}
	if err := m.DeleteGroup("备用组"); err != nil || len(m.Groups()) != 0 {
		t.Fatalf("delete: %v", err)
	}
	if err := m.DeleteGroup("备用组"); err == nil {
		t.Fatal("删除不存在的模型组应报错")
	}
}

// 端到端：第一个成员被限流 → 自动转交给第二个；账本记两条；成员 a 进入冷却，修改配置档后恢复。
func TestGroupFailoverThroughManager(t *testing.T) {
	var na, nb atomic.Int32
	a, b := chatServer(t, 429, "", &na), chatServer(t, 200, "来自B", &nb)
	m, eng, _ := newEnv(t)
	mem := store.NewMemory()
	m.SetUsage(usage.NewWriter(mem, nil), mem)
	addOpenAI(t, m, "a", a.URL, nil)
	addOpenAI(t, m, "b", b.URL, nil)
	if err := m.SaveGroup(GroupInput{GroupConfig: provider.GroupConfig{Name: "主力", Members: []provider.GroupMember{{Profile: "a", Model: "m"}, {Profile: "b", Model: "m"}}, Strategy: provider.StrategyOrder}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Select("主力", provider.GroupModel); err != nil {
		t.Fatal(err)
	}
	rec := &event.Recorder{}
	eng.Bus().Subscribe(rec.Handle)

	if _, err := eng.Send(context.Background(), "你好"); err != nil {
		t.Fatal(err)
	}
	var route event.Route
	for _, ev := range rec.Events() {
		if r, ok := ev.Data.(event.Route); ok {
			route = r
		}
	}
	if route.Profile != "b" || route.Try != 2 || len(route.HandedOn) != 1 || route.HandedOn[0].Kind != "rate_limit" {
		t.Fatalf("route: %+v", route)
	}
	if na.Load() != 1 || nb.Load() != 1 {
		t.Fatalf("各调用一次: a=%d b=%d", na.Load(), nb.Load())
	}

	// a 在冷却中：第二次发送不再请求它。
	if _, err := eng.Send(context.Background(), "再来一次"); err != nil {
		t.Fatal(err)
	}
	if na.Load() != 1 || nb.Load() != 2 {
		t.Fatalf("冷却期间不应再试 a: a=%d b=%d", na.Load(), nb.Load())
	}
	sts := m.GroupStatuses()
	if len(sts) != 1 || sts[0].Members[0].State != provider.StateCooling || sts[0].Members[1].State != provider.StateHealthy {
		t.Fatalf("状态: %+v", sts)
	}
	// 手动恢复。
	m.ResetMember("a", "m")
	if m.GroupStatuses()[0].Members[0].State != provider.StateHealthy {
		t.Fatal("恢复后应为健康")
	}
	// 重新保存配置档（比如换了密钥）也会清掉旧的失败记录。
	m.health.Fail(provider.MemberKey("a", "m"), provider.KindAuth, 0, "bad key")
	if err := m.Save(ProviderInput{OriginalName: "a", Name: "a", Type: "openai", BaseURL: a.URL, APIKey: "sk-new", Models: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	if m.GroupStatuses()[0].Members[0].State != provider.StateHealthy {
		t.Fatal("修改配置档后应恢复")
	}

	// 账本：写入是异步的，Close 之前等一下。
	time.Sleep(50 * time.Millisecond)
	rows, _ := mem.UsageRows(context.Background(), usage.Query{By: usage.ByModel})
	got := map[string]usage.Row{}
	for _, r := range rows {
		got[r.Profile] = r
	}
	if got["b"].Requests != 2 || got["b"].Input != 20 || got["a"].FailedTries != 1 || got["a"].HandedOn != 1 || got["a"].Requests != 0 {
		t.Fatalf("账本: %+v", got)
	}
}

func TestProxyIsMaskedAndRestored(t *testing.T) {
	var n atomic.Int32
	srv := chatServer(t, 200, "x", &n)
	m, _, path := newEnv(t)

	in := ProviderInput{Name: "p", Type: "openai", BaseURL: srv.URL, APIKey: "sk-p", Models: []string{"m"}, Options: map[string]any{"proxy": "http://alice:s3cret@127.0.0.1:9"}}
	if err := m.Save(in); err != nil {
		t.Fatal(err)
	}
	view := func() map[string]any {
		for _, p := range m.Providers() {
			if p.Name == "p" {
				return p.Options
			}
		}
		return nil
	}
	shown, _ := view()["proxy"].(string)
	if strings.Contains(shown, "s3cret") || !strings.Contains(shown, "alice") {
		t.Fatalf("发给前端的代理地址必须脱敏: %q", shown)
	}
	// 前端把脱敏的地址原样提交回来（用户只改了模型列表）：保存的仍是完整地址。
	again := ProviderInput{OriginalName: "p", Name: "p", Type: "openai", BaseURL: srv.URL, Models: []string{"m", "m2"}, Options: map[string]any{"proxy": shown}}
	if err := m.Save(again); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "http://alice:s3cret@127.0.0.1:9") || strings.Contains(string(raw), "***") {
		t.Fatalf("应保留完整地址，而不是脱敏后的:\n%s", raw)
	}
	for _, p := range m.Profiles() {
		if p.Name == "p" {
			if got, _ := p.Options["proxy"].(string); got != "http://alice:s3cret@127.0.0.1:9" {
				t.Fatalf("运行时应使用完整地址: %q", got)
			}
		}
	}
	// 非法代理保存时就报错，而不是等到请求失败。
	bad := ProviderInput{Name: "q", Type: "openai", BaseURL: srv.URL, APIKey: "k", Models: []string{"m"}, Options: map[string]any{"proxy": "ftp://x"}}
	if err := m.Save(bad); err == nil {
		t.Fatal("非法代理应拒绝")
	}
	// 清空代理 = 不设置。
	again.Options = map[string]any{"proxy": ""}
	if err := m.Save(again); err != nil {
		t.Fatal(err)
	}
	if _, has := view()["proxy"]; has {
		t.Fatal("清空后不应保留 proxy 字段")
	}
}

func TestGlobalProxyAppliesOnlyWhereNotOverridden(t *testing.T) {
	var n atomic.Int32
	srv := chatServer(t, 200, "x", &n)
	m, _, _ := newEnv(t)
	addOpenAI(t, m, "plain", srv.URL, nil)
	if err := m.Save(ProviderInput{Name: "own", Type: "openai", BaseURL: srv.URL, APIKey: "k", Models: []string{"m"}, Options: map[string]any{"proxy": "direct"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetProxy("socks5://u:pw@127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	if v := m.Proxy(); !v.Set || strings.Contains(v.Display, "pw") {
		t.Fatalf("全局代理视图要脱敏: %+v", v)
	}
	px := map[string]string{}
	for _, p := range m.Profiles() {
		s, _ := p.Options["proxy"].(string)
		px[p.Name] = s
	}
	if px["plain"] != "socks5://u:pw@127.0.0.1:1080" || px["own"] != "direct" || px["fake"] != "socks5://u:pw@127.0.0.1:1080" {
		t.Fatalf("全局默认只填到没有单独设置的配置档: %+v", px)
	}
	// 提交脱敏后的地址表示不修改。
	if err := m.SetProxy(m.Proxy().Display); err != nil {
		t.Fatal(err)
	}
	if m.file.Proxy != "socks5://u:pw@127.0.0.1:1080" {
		t.Fatalf("不应把密码覆盖成 ***: %q", m.file.Proxy)
	}
	if err := m.SetProxy("not a proxy"); err == nil {
		t.Fatal("非法全局代理应拒绝")
	}
	if err := m.SetProxy(""); err != nil || m.Proxy().Set {
		t.Fatal("清空全局代理")
	}
}

func TestUsageReportPricingAndRanges(t *testing.T) {
	var n atomic.Int32
	srv := chatServer(t, 200, "x", &n)
	m, _, _ := newEnv(t)
	mem := store.NewMemory()
	m.SetUsage(usage.NewWriter(mem, nil), mem)
	addOpenAI(t, m, "priced", srv.URL, map[string]any{"m": map[string]any{"input_price": 3.0, "output_price": 15.0, "cache_read_price": 0.3}})
	addOpenAI(t, m, "free", srv.URL, nil)

	now := time.Now()
	add := func(profile string, daysAgo int, in, out, cr int) {
		mem.AppendUsage(provider.UsageEvent{Time: now.AddDate(0, 0, -daysAgo), Profile: profile, Model: "m", Status: provider.StatusOK, TryNo: 1,
			Usage: provider.Usage{InputTokens: in, OutputTokens: out, CacheReadTokens: cr}})
	}
	add("priced", 0, 1000, 100, 400)
	add("priced", 3, 2000, 200, 0)
	add("free", 0, 500, 50, 0)
	add("priced", 40, 9000, 900, 0)

	get := func(req UsageRequest) UsageReportView {
		v, err := m.UsageReport(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	today := get(UsageRequest{Range: "today"})
	// 缓存价没填写的部分按输入价计；这里 cache_read_price=0.3，cache_write_price 没填 → 按输入价（但本例无缓存写）。
	want := (600*3.0 + 400*0.3 + 100*15.0) / 1e6
	if len(today.Nodes) != 1 || today.Totals.Requests != 2 || !near(today.Totals.CostUSD, want) || today.Totals.UnpricedCalls != 1 {
		t.Fatalf("today: %+v", today.Totals)
	}
	if len(today.UnpricedModels) != 1 || today.UnpricedModels[0] != "free/m" {
		t.Fatalf("没有价格的模型: %v", today.UnpricedModels)
	}
	if w := get(UsageRequest{Range: "7d"}); w.Totals.Requests != 3 || len(w.Nodes) != 2 {
		t.Fatalf("7d: %+v", w.Totals)
	}
	if w := get(UsageRequest{Range: "30d"}); w.Totals.Requests != 3 {
		t.Fatalf("30d 不含 40 天前: %+v", w.Totals)
	}
	if w := get(UsageRequest{Range: "all"}); w.Totals.Requests != 4 || w.From != "" {
		t.Fatalf("all: %+v", w.Totals)
	}
	if f := get(UsageRequest{Range: "all", Profile: "free"}); f.Totals.Requests != 1 || len(f.Profiles) != 2 {
		t.Fatalf("按配置档筛选，下拉框仍列出全部配置档: %+v %v", f.Totals, f.Profiles)
	}
	if c := get(UsageRequest{Range: "custom", From: usage.Day(now.AddDate(0, 0, -4)), To: usage.Day(now.AddDate(0, 0, -2))}); c.Totals.Requests != 1 {
		t.Fatalf("custom: %+v", c.Totals)
	}
	for _, bad := range []UsageRequest{{Range: "weird"}, {Range: "custom", From: "yesterday"}, {Range: "custom", From: "2026-10-25", To: "2026-10-01"}, {By: "colour"}} {
		if _, err := m.UsageReport(context.Background(), bad); err == nil {
			t.Errorf("%+v 应报错", bad)
		}
	}
	csv, err := m.ExportUsageCSV(context.Background(), UsageRequest{Range: "all"})
	if err != nil || strings.Count(csv, "\n") != 5 {
		t.Fatalf("csv: %v\n%s", err, csv)
	}
}

func near(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }
