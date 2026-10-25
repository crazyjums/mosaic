package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/usage"
)

// 用量账本的存储。表结构见 sqlite.go 的迁移 3，聚合逻辑的语义见 internal/usage。

var (
	_ usage.Source = (*SQLite)(nil)
	_ usage.Sink   = (*SQLite)(nil)
	_ usage.Source = (*Memory)(nil)
	_ usage.Sink   = (*Memory)(nil)
)

// AppendUsage 写入一条记录。
func (s *SQLite) AppendUsage(ev provider.UsageEvent) error {
	_, err := s.db.Exec(`INSERT INTO usage_events
		(ts, day, session_id, source, profile, model, group_name, try_no, status, error_kind,
		 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, duration_ms, ttft_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ev.Time.UnixMilli(), usage.Day(ev.Time), ev.SessionID, ev.Source, ev.Profile, ev.Model, ev.Group, ev.TryNo, ev.Status, string(ev.ErrorKind),
		ev.Usage.InputTokens, ev.Usage.OutputTokens, ev.Usage.CacheReadTokens, ev.Usage.CacheWriteTokens, ev.DurationMS, ev.TTFTMS)
	if err != nil {
		return fmt.Errorf("store: append usage: %w", err)
	}
	return nil
}

// where 把 Query 变成 WHERE 子句（列名带 e. 前缀）。
func where(q usage.Query) (string, []any) {
	var cond []string
	var args []any
	add := func(c string, v any) { cond = append(cond, c); args = append(args, v) }
	if q.From != "" {
		add("e.day >= ?", q.From)
	}
	if q.To != "" {
		add("e.day <= ?", q.To)
	}
	if q.Profile != "" {
		add("e.profile = ?", q.Profile)
	}
	if q.Model != "" {
		add("e.model = ?", q.Model)
	}
	if q.Group != "" {
		add("e.group_name = ?", q.Group)
	}
	if len(cond) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(cond, " AND "), args
}

// UsageRows 在 SQL 里聚合。分组键与 usage.Aggregate 一致。
func (s *SQLite) UsageRows(ctx context.Context, q usage.Query) ([]usage.Row, error) {
	dayCol, sessCol, group := "''", "''", "e.profile, e.model"
	switch q.By {
	case usage.BySession:
		sessCol, group = "e.session_id", "e.session_id, e.profile, e.model"
	case usage.ByModel, usage.ByProfile:
	default:
		dayCol, group = "e.day", "e.day, e.profile, e.model"
	}
	w, args := where(q)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+dayCol+`, `+sessCol+`, COALESCE(MAX(s.title), ''), e.profile, e.model,
		       COALESCE(SUM(e.status = 'ok'), 0),
		       COALESCE(SUM(e.status IN ('error', 'handed_on')), 0),
		       COALESCE(SUM(e.status = 'handed_on'), 0),
		       COALESCE(SUM(e.input_tokens), 0), COALESCE(SUM(e.output_tokens), 0),
		       COALESCE(SUM(e.cache_read_tokens), 0), COALESCE(SUM(e.cache_write_tokens), 0),
		       COALESCE(SUM(e.duration_ms), 0)
		FROM usage_events e LEFT JOIN sessions s ON s.id = e.session_id`+w+`
		GROUP BY `+group+`
		ORDER BY 1 DESC, 2, e.profile, e.model`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: usage rows: %w", err)
	}
	defer rows.Close()
	var out []usage.Row
	for rows.Next() {
		var r usage.Row
		if err := rows.Scan(&r.Day, &r.SessionID, &r.SessionTitle, &r.Profile, &r.Model, &r.Requests, &r.FailedTries, &r.HandedOn,
			&r.Input, &r.Output, &r.CacheRead, &r.CacheWrite, &r.DurationMS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UsageEvents 返回原始记录（导出用），按时间升序。
func (s *SQLite) UsageEvents(ctx context.Context, q usage.Query) ([]usage.Event, error) {
	w, args := where(q)
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.ts, e.day, e.session_id, e.source, e.profile, e.model, e.group_name, e.try_no, e.status, e.error_kind,
		       e.input_tokens, e.output_tokens, e.cache_read_tokens, e.cache_write_tokens, e.duration_ms, e.ttft_ms
		FROM usage_events e`+w+` ORDER BY e.ts, e.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: usage events: %w", err)
	}
	defer rows.Close()
	var out []usage.Event
	for rows.Next() {
		var e usage.Event
		var ts int64
		var kind string
		if err := rows.Scan(&ts, &e.Day, &e.SessionID, &e.Source, &e.Profile, &e.Model, &e.Group, &e.TryNo, &e.Status, &kind,
			&e.Usage.InputTokens, &e.Usage.OutputTokens, &e.Usage.CacheReadTokens, &e.Usage.CacheWriteTokens, &e.DurationMS, &e.TTFTMS); err != nil {
			return nil, err
		}
		e.Time, e.ErrorKind = time.UnixMilli(ts), provider.ErrorKind(kind)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ProfileTokens 返回某一天各配置档用掉的 token（输入 + 输出）。
func (s *SQLite) ProfileTokens(ctx context.Context, day string) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT profile, SUM(input_tokens + output_tokens) FROM usage_events WHERE day = ? GROUP BY profile`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var p string
		var n int64
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

// ---- 内存实现：测试和 CLI 的 --no-store 模式 ----

type memUsage struct {
	mu     sync.Mutex
	events []usage.Event
}

func (m *Memory) usage() *memUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.use == nil {
		m.use = &memUsage{}
	}
	return m.use
}

func (m *Memory) AppendUsage(ev provider.UsageEvent) error {
	u := m.usage()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.events = append(u.events, usage.Event{UsageEvent: ev, Day: usage.Day(ev.Time)})
	return nil
}

func (m *Memory) titles() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := map[string]string{}
	for id, s := range m.sessions {
		t[id] = s.Title
	}
	return t
}

func (m *Memory) UsageRows(_ context.Context, q usage.Query) ([]usage.Row, error) {
	u := m.usage()
	titles := m.titles()
	u.mu.Lock()
	defer u.mu.Unlock()
	return usage.Aggregate(u.events, q, titles), nil
}

func (m *Memory) UsageEvents(_ context.Context, q usage.Query) ([]usage.Event, error) {
	u := m.usage()
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []usage.Event
	for _, e := range u.events {
		if q.Match(e.Day, e.UsageEvent) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *Memory) ProfileTokens(_ context.Context, day string) (map[string]int64, error) {
	u := m.usage()
	u.mu.Lock()
	defer u.mu.Unlock()
	out := map[string]int64{}
	for _, e := range u.events {
		if e.Day == day {
			out[e.Profile] += int64(e.Usage.InputTokens + e.Usage.OutputTokens)
		}
	}
	return out, nil
}
