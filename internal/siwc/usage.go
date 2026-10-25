package siwc

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// 套餐用量统计（状态栏的"ChatGPT 额度"）。
//
// OpenAI 目前没有提供用 Sign in with ChatGPT 令牌查询剩余额度的接口（官方答复"暂不支持"，
// 请求响应里也没有额度相关的头），所以这里只能做两件诚实的事：
//   - 记录本应用每次调用用掉的 token，按套餐限额的两个窗口（5 小时、7 天）汇总；
//   - 记住最近一次"额度已用完"错误，下一次成功调用后清除。
//
// 记录写在 chatgpt/usage.jsonl（每行一条，追加写入；桌面端和 CLI 可以同时写），
// 查询时重新读文件，所以两个进程看到的是同一份数据。启动时删除 7 天以前的记录。

const usageRetention = 7 * 24 * time.Hour

// UsageWindow 是一个时间窗口内的用量。
type UsageWindow struct {
	Requests     int `json:"requests"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CachedTokens int `json:"cached_tokens"`
}

// UsageSummary 是界面上显示的用量摘要。
type UsageSummary struct {
	Last5h UsageWindow `json:"last_5h"`
	Last7d UsageWindow `json:"last_7d"`
	// LimitHitAt 非零表示最近一次调用遇到了"额度已用完"，之后还没有成功的调用。
	LimitHitAt   time.Time `json:"limit_hit_at,omitzero"`
	LimitMessage string    `json:"limit_message,omitempty"`
}

type usageRecord struct {
	Time   time.Time `json:"t"`
	Model  string    `json:"model,omitempty"`
	Input  int       `json:"in"`
	Output int       `json:"out"`
	Cached int       `json:"cached,omitempty"`
	// Limit 非空表示这条是"额度已用完"的记录（没有用量）。
	Limit string `json:"limit,omitempty"`
}

type usageLedger struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
}

func (l *usageLedger) append(r usageRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return // 统计失败不能影响对话
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func (l *usageLedger) read() []usageRecord {
	f, err := os.Open(l.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []usageRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r usageRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

// prune 删除过期记录（启动时调用一次）。
func (l *usageLedger) prune() {
	recs := l.read()
	cutoff := l.now().Add(-usageRetention)
	keep := recs[:0]
	for _, r := range recs {
		if r.Time.After(cutoff) {
			keep = append(keep, r)
		}
	}
	if len(keep) == len(recs) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	tmp := l.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	enc := json.NewEncoder(f)
	for _, r := range keep {
		_ = enc.Encode(r)
	}
	if f.Close() == nil {
		_ = os.Rename(tmp, l.path)
	}
}

func (l *usageLedger) summary() UsageSummary {
	now := l.now()
	var s UsageSummary
	for _, r := range l.read() {
		if r.Limit != "" {
			s.LimitHitAt, s.LimitMessage = r.Time, r.Limit
			continue
		}
		if !s.LimitHitAt.IsZero() && r.Time.After(s.LimitHitAt) {
			s.LimitHitAt, s.LimitMessage = time.Time{}, "" // 额度用完之后又成功调用过：已经恢复
		}
		add := func(w *UsageWindow) {
			w.Requests++
			w.InputTokens += r.Input
			w.OutputTokens += r.Output
			w.CachedTokens += r.Cached
		}
		if now.Sub(r.Time) <= 5*time.Hour {
			add(&s.Last5h)
		}
		if now.Sub(r.Time) <= usageRetention {
			add(&s.Last7d)
		}
	}
	return s
}

// ---- Manager 的公开方法（chatgpt 适配器通过可选接口调用） ----

// ReportUsage 记录一次成功调用的用量。
func (m *Manager) ReportUsage(model string, input, output, cached int) {
	m.usage.append(usageRecord{Time: m.now(), Model: model, Input: input, Output: output, Cached: cached})
}

// ReportLimit 记录一次"额度已用完"错误。
func (m *Manager) ReportLimit(message string) {
	if message == "" {
		message = "usage limit exceeded"
	}
	m.usage.append(usageRecord{Time: m.now(), Limit: message})
}

// Usage 返回本应用最近 5 小时 / 7 天用掉的套餐额度（token 数），以及是否遇到过额度用完。
func (m *Manager) Usage() UsageSummary { return m.usage.summary() }
