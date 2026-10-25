package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/usage"
)

type usageStore interface {
	usage.Source
	usage.Sink
}

func ev(t time.Time, session, profile, model, group, status string, in, out, cr, cw int) provider.UsageEvent {
	return provider.UsageEvent{
		Time: t, SessionID: session, Source: provider.SourceChat, Profile: profile, Model: model, Group: group, TryNo: 1, Status: status,
		Usage:      provider.Usage{InputTokens: in, OutputTokens: out, CacheReadTokens: cr, CacheWriteTokens: cw},
		DurationMS: 1000, TTFTMS: 200,
	}
}

// 两种实现跑同一组用例：SQL 聚合和内存聚合必须给出相同的结果。
func testUsage(t *testing.T, st usageStore, saveSession func(id, title string)) {
	ctx := context.Background()
	saveSession("s1", "第一个会话")
	loc := time.FixedZone("UTC+8", 8*3600)
	defer func(l *time.Location) { usage.Location = l }(usage.Location)
	usage.Location = loc

	// 本地时间 10-24 23:30 和 10-25 00:30 虽然只差一小时，但属于不同的"天"。
	d1 := time.Date(2026, 10, 24, 23, 30, 0, 0, loc)
	d2 := time.Date(2026, 10, 25, 0, 30, 0, 0, loc)
	for _, e := range []provider.UsageEvent{
		ev(d1, "s1", "anthropic", "sonnet", "", provider.StatusOK, 1000, 100, 400, 0),
		ev(d2, "s1", "anthropic", "sonnet", "", provider.StatusOK, 2000, 200, 1000, 0),
		ev(d2.Add(time.Minute), "s1", "deepseek", "v4", "主力", provider.StatusHandedOn, 0, 0, 0, 0),
		ev(d2.Add(2*time.Minute), "s1", "deepseek", "v4", "主力", provider.StatusError, 0, 0, 0, 0),
		ev(d2.Add(3*time.Minute), "s2", "deepseek", "v4", "主力", provider.StatusOK, 500, 50, 0, 0),
		ev(d2.Add(4*time.Minute), "", "deepseek", "v4", "", provider.StatusCancelled, 10, 1, 0, 0),
	} {
		if err := st.AppendUsage(e); err != nil {
			t.Fatal(err)
		}
	}

	byDay, err := st.UsageRows(ctx, usage.Query{By: usage.ByDay})
	if err != nil {
		t.Fatal(err)
	}
	if len(byDay) != 3 || byDay[0].Day != "2026-10-25" || byDay[2].Day != "2026-10-24" {
		t.Fatalf("按本地日期分组且最新的在前: %+v", byDay)
	}
	d25 := map[string]usage.Row{}
	for _, r := range byDay {
		if r.Day == "2026-10-25" {
			d25[r.Profile+"/"+r.Model] = r
		}
	}
	a, d := d25["anthropic/sonnet"], d25["deepseek/v4"]
	if a.Requests != 1 || a.Input != 2000 || a.CacheRead != 1000 {
		t.Fatalf("anthropic: %+v", a)
	}
	// 请求数只数成功的；已转交和失败的尝试单独算；取消的不算请求也不算失败，但 token 计入。
	if d.Requests != 1 || d.FailedTries != 2 || d.HandedOn != 1 || d.Input != 510 || d.Output != 51 {
		t.Fatalf("deepseek: %+v", d)
	}

	// 筛选：模型组、日期范围、配置档。
	g, _ := st.UsageRows(ctx, usage.Query{By: usage.ByModel, Group: "主力"})
	if len(g) != 1 || g[0].Requests != 1 || g[0].FailedTries != 2 {
		t.Fatalf("按组筛选: %+v", g)
	}
	r, _ := st.UsageRows(ctx, usage.Query{By: usage.ByModel, From: "2026-10-25", To: "2026-10-25", Profile: "anthropic"})
	if len(r) != 1 || r[0].Input != 2000 || r[0].Day != "" {
		t.Fatalf("日期和配置档筛选，按模型分组时不带日期: %+v", r)
	}

	// 按会话：带标题；没有会话的记录归到空会话。
	bySess, _ := st.UsageRows(ctx, usage.Query{By: usage.BySession})
	titles := map[string]string{}
	for _, r := range bySess {
		titles[r.SessionID] = r.SessionTitle
	}
	if titles["s1"] != "第一个会话" || titles["s2"] != "" {
		t.Fatalf("会话标题: %+v", titles)
	}

	// 原始记录按时间升序，day 已算好。
	events, err := st.UsageEvents(ctx, usage.Query{})
	if err != nil || len(events) != 6 || events[0].Day != "2026-10-24" || !events[0].Time.Equal(d1) || events[3].ErrorKind != "" {
		t.Fatalf("events: %v %+v", err, events)
	}
	var csv strings.Builder
	if err := usage.WriteCSV(&csv, events); err != nil || !strings.Contains(csv.String(), "input_tokens") || strings.Count(csv.String(), "\n") != 7 {
		t.Fatalf("csv: %v\n%s", err, csv.String())
	}

	tokens, _ := st.ProfileTokens(ctx, "2026-10-25")
	if !reflect.DeepEqual(tokens, map[string]int64{"anthropic": 2200, "deepseek": 561}) {
		t.Fatalf("今日 token: %v", tokens)
	}
}

func TestUsageSQLite(t *testing.T) {
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	testUsage(t, st, func(id, title string) {
		s := sampleSession(id)
		s.Title = title
		if err := st.SaveSession(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	})
	// 删除会话不抹掉用量统计。
	if err := st.DeleteSession(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.UsageRows(context.Background(), usage.Query{By: usage.ByModel})
	if len(rows) != 2 {
		t.Fatalf("删除会话后用量应保留: %+v", rows)
	}
}

func TestUsageMemory(t *testing.T) {
	m := NewMemory()
	testUsage(t, m, func(id, title string) {
		s := sampleSession(id)
		s.Title = title
		m.SaveSession(context.Background(), s)
	})
}

// 旧数据库（只有迁移 1、2）升级后能用，已有的会话不受影响。
func TestUsageMigrationKeepsSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	st, _ := OpenSQLite(path)
	st.SaveSession(context.Background(), sampleSession("old"))
	st.Close()
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.LoadSession(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendUsage(ev(time.Now(), "old", "p", "m", "", provider.StatusOK, 1, 1, 0, 0)); err != nil {
		t.Fatal(err)
	}
}
