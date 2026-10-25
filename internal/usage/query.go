package usage

import (
	"context"
	"sort"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// Location 是"一天"的划分依据（本地时区）。写入时按它算好 day，之后换时区也不会改写历史。测试里可以替换。
var Location = time.Local

// Day 返回时间所在的本地日期，格式 YYYY-MM-DD。
func Day(t time.Time) string { return t.In(Location).Format("2006-01-02") }

// 分组方式。
const (
	ByDay     = "day"
	BySession = "session"
	ByModel   = "model"
	ByProfile = "profile"
)

// Query 是查询条件。日期是闭区间，空表示不限。
type Query struct {
	From, To string
	Profile  string
	Model    string
	Group    string // 模型组名
	By       string
}

// Row 是聚合后的一行。By=day 时按 (日期, 配置档, 模型) 聚合，By=session 时按 (会话, 配置档, 模型)，
// By=model / profile 时按 (配置档, 模型) 聚合（Day 和 SessionID 为空）。
type Row struct {
	Day          string
	SessionID    string
	SessionTitle string
	Profile      string
	Model        string

	Requests    int // 成功的调用
	FailedTries int // 失败的尝试（含已转交的）
	HandedOn    int // 其中已转交给其他成员的

	Input, Output, CacheRead, CacheWrite int64
	DurationMS                           int64
}

// Source 是账本的存储。SQLite 和内存实现都满足它。
type Source interface {
	// UsageRows 按 Query 聚合。
	UsageRows(ctx context.Context, q Query) ([]Row, error)
	// UsageEvents 返回原始记录（导出 CSV 用），按时间升序。
	UsageEvents(ctx context.Context, q Query) ([]Event, error)
	// ProfileTokens 返回某一天各配置档用掉的 token（输入 + 输出），模型组的 least_used 策略用。
	ProfileTokens(ctx context.Context, day string) (map[string]int64, error)
}

// Event 是一条原始记录：UsageEvent 加上写入时算好的日期。
type Event struct {
	provider.UsageEvent
	Day string
}

// Match 判断事件是否满足查询条件（内存实现用；SQL 里是等价的 WHERE）。
func (q Query) Match(day string, e provider.UsageEvent) bool {
	return (q.From == "" || day >= q.From) && (q.To == "" || day <= q.To) &&
		(q.Profile == "" || e.Profile == q.Profile) && (q.Model == "" || e.Model == q.Model) && (q.Group == "" || e.Group == q.Group)
}

// Aggregate 在内存里按 Query 聚合事件。titles 把会话 ID 映射到标题（可以为 nil）。
func Aggregate(events []Event, q Query, titles map[string]string) []Row {
	type key struct{ day, session, profile, model string }
	acc := map[key]*Row{}
	for _, ev := range events {
		if !q.Match(ev.Day, ev.UsageEvent) {
			continue
		}
		k := key{profile: ev.Profile, model: ev.Model}
		switch q.By {
		case BySession:
			k.session = ev.SessionID
		case ByModel, ByProfile:
		default:
			k.day = ev.Day
		}
		r := acc[k]
		if r == nil {
			r = &Row{Day: k.day, SessionID: k.session, Profile: ev.Profile, Model: ev.Model, SessionTitle: titles[k.session]}
			acc[k] = r
		}
		switch ev.Status {
		case provider.StatusOK:
			r.Requests++
		case provider.StatusError, provider.StatusHandedOn:
			r.FailedTries++
			if ev.Status == provider.StatusHandedOn {
				r.HandedOn++
			}
		}
		r.Input += int64(ev.Usage.InputTokens)
		r.Output += int64(ev.Usage.OutputTokens)
		r.CacheRead += int64(ev.Usage.CacheReadTokens)
		r.CacheWrite += int64(ev.Usage.CacheWriteTokens)
		r.DurationMS += ev.DurationMS
	}
	out := make([]Row, 0, len(acc))
	for _, r := range acc {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Day != b.Day {
			return a.Day > b.Day
		}
		if a.SessionID != b.SessionID {
			return a.SessionID < b.SessionID
		}
		if a.Profile != b.Profile {
			return a.Profile < b.Profile
		}
		return a.Model < b.Model
	})
	return out
}
