package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// 模型组：一个虚拟的厂商，由若干个"配置档 + 模型"按顺序组成，请求失败时自动换下一个成员。
//
// 它自己实现 Provider，所以内核（agent 循环、工具、权限）完全不知道背后有几个厂商——
// 这也是选择"进程内故障转移"而不是"本地网关"的原因：本项目自己就是 agent，不需要在多种协议之间转换。
//
// 核心规则：只有成员在"还没有输出任何内容"就失败时才自动切换。已经向界面输出了文字、思考或工具调用，
// 再换成员会造成内容重复、断句错乱，甚至工具调用被执行两次；这时把错误交给上层，由用户决定要不要重试。

// GroupType 是模型组配置档的 Type。它不在 Registry 里注册：模型组需要引用其他配置档，只有 Catalog 能装配它。
const GroupType = "group"

// GroupModel 是模型组对外暴露的唯一"模型名"。请求里的模型名会被忽略，每个成员用自己配置的模型。
const GroupModel = "auto"

// 策略。
const (
	StrategyOrder     = "order"      // 永远从第一个可用成员开始（主力 + 备用）
	StrategyRotate    = "rotate"     // 每个新会话换下一个成员（分摊多个账号的额度）
	StrategyLeastUsed = "least_used" // 优先今天用得最少的成员
)

// GroupMember 是模型组的一个成员。
type GroupMember struct {
	Profile string `json:"profile"`
	Model   string `json:"model"`
}

// GroupConfig 是模型组的配置。
type GroupConfig struct {
	Name     string        `json:"name"`
	Members  []GroupMember `json:"members"`
	Strategy string        `json:"strategy"`
	// Sticky 为 true 时，同一会话优先沿用上一个应答的成员：厂商侧的提示缓存按账号生效，来回换会让缓存全部失效。
	Sticky bool `json:"sticky"`
	// FirstByteTimeoutS 是等待首个响应事件的秒数，超过按网络故障处理。0 表示不限制。
	FirstByteTimeoutS int `json:"first_byte_timeout_s"`
}

// DefaultFirstByteTimeoutS 是新建模型组时的默认值。只作用于"第一个事件"，不是整个响应的超时。
const DefaultFirstByteTimeoutS = 30

// Validate 检查配置是否合法。
func (c GroupConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("请填写模型组名称")
	}
	if len(c.Members) == 0 {
		return errors.New("模型组至少需要一个成员")
	}
	seen := map[string]bool{}
	for _, m := range c.Members {
		if m.Profile == "" || m.Model == "" {
			return errors.New("模型组成员需要同时指定厂商和模型")
		}
		if seen[MemberKey(m.Profile, m.Model)] {
			return fmt.Errorf("成员 %s / %s 重复了", m.Profile, m.Model)
		}
		seen[MemberKey(m.Profile, m.Model)] = true
	}
	switch c.Strategy {
	case "", StrategyOrder, StrategyRotate, StrategyLeastUsed:
	default:
		return fmt.Errorf("未知的策略 %q", c.Strategy)
	}
	if c.FirstByteTimeoutS < 0 || c.FirstByteTimeoutS > 600 {
		return errors.New("首字节超时应在 0～600 秒之间")
	}
	return nil
}

type groupMember struct {
	GroupMember
	key       string
	leaf      Provider // 没有计量包装的原始实例：尝试的记账由模型组自己做，才能标出"已转交"
	ctxWindow int
	// cacheSeparate：成员的输入 token 不含缓存部分（Anthropic），记账时要换算，见 normalizeUsage。
	cacheSeparate bool
	// unavailable 非空表示成员本身不可用（引用的配置档不存在、缺密钥……）。
	unavailable string
}

// Group 是模型组的实现。
type Group struct {
	cfg     GroupConfig
	members []*groupMember
	health  *Health
	rec     UsageRecorder
	// firstByte 是等待首个事件的超时；0 表示不限制。
	firstByte time.Duration
	// todayTokens 返回某个配置档今天用掉的 token（least_used 策略用）；可以为 nil。
	todayTokens func(profile string) int64

	mu         sync.Mutex
	rr         int               // rotate 策略的计数器
	lastServed string            // 最近一次应答的成员；它的思考块签名对它自己才有效
	sticky     map[string]string // 会话 → 上次应答的成员
}

var _ Provider = (*Group)(nil)

func (g *Group) Name() string { return g.cfg.Name }

// AllFailedError 表示模型组里所有成员都失败（或都不可用）。
type AllFailedError struct {
	Group    string
	Attempts []Attempt
	// Skipped 是因为冷却 / 停用而没有尝试的成员的说明。
	Skipped []string
	last    error
}

func (e *AllFailedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "模型组 %s 的成员都不可用", e.Group)
	for _, a := range e.Attempts {
		fmt.Fprintf(&b, "\n  · %s / %s 失败（%s）：%s", a.Profile, a.Model, a.Kind, a.Message)
	}
	for _, s := range e.Skipped {
		fmt.Fprintf(&b, "\n  · %s", s)
	}
	return b.String()
}
func (e *AllFailedError) Unwrap() error { return e.last }

// order 返回按策略排好的成员顺序（不考虑健康状态）。
func (g *Group) order(sessionID string) []*groupMember {
	out := make([]*groupMember, 0, len(g.members))
	out = append(out, g.members...)
	switch g.cfg.Strategy {
	case StrategyRotate:
		g.mu.Lock()
		start := g.rr % len(out)
		_, known := g.sticky[sessionID]
		if !g.cfg.Sticky || !known {
			g.rr++ // 新会话（或不粘性）才推进，同一会话内保持稳定
		}
		g.mu.Unlock()
		out = append(out[start:], out[:start]...)
	case StrategyLeastUsed:
		if g.todayTokens != nil {
			sort.SliceStable(out, func(i, j int) bool {
				return g.todayTokens(out[i].Profile) < g.todayTokens(out[j].Profile)
			})
		}
	}
	if g.cfg.Sticky && sessionID != "" {
		g.mu.Lock()
		prev := g.sticky[sessionID]
		g.mu.Unlock()
		for i, m := range out {
			if m.key == prev && i > 0 {
				out = append([]*groupMember{m}, append(out[:i:i], out[i+1:]...)...)
				break
			}
		}
	}
	return out
}

// pick 在 order 里选下一个可以尝试的成员：跳过已试过的、不可用的、上下文窗口不够大的、仍在冷却的。
// 全都不行时，强制放行冷却时间最早到期的一个（已停用的除外），保证请求至少有一次机会。
func (g *Group) pick(order []*groupMember, tried map[string]bool, minWindow int, first bool) *groupMember {
	for _, m := range order {
		if tried[m.key] || m.leaf == nil {
			continue
		}
		if minWindow > 0 && m.ctxWindow > 0 && m.ctxWindow <= minWindow {
			continue
		}
		if g.health.acquire(m.key) {
			return m
		}
	}
	if !first {
		return nil
	}
	var keys []string
	byKey := map[string]*groupMember{}
	for _, m := range order {
		if !tried[m.key] && m.leaf != nil {
			keys = append(keys, m.key)
			byKey[m.key] = m
		}
	}
	if k, ok := g.health.force(keys); ok {
		return byKey[k]
	}
	return nil
}

// stripThinking 去掉历史里的思考块。
// 思考块带的签名只对产生它的模型有效，发给别的成员会被拒绝；它本来也只用于回显，丢掉不影响内容。
func stripThinking(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		var blocks []Block
		changed := false
		for _, b := range m.Content {
			if b.Type == BlockThinking || b.Type == BlockRedactedThinking {
				changed = true
				continue
			}
			blocks = append(blocks, b)
		}
		if !changed {
			out = append(out, m)
		} else if len(blocks) > 0 {
			out = append(out, Message{Role: m.Role, Content: blocks})
		}
	}
	return out
}

func (g *Group) record(ev UsageEvent, cacheSeparate bool) {
	ev.Usage = normalizeUsage(ev.Usage, cacheSeparate)
	if g.rec != nil {
		g.rec.RecordUsage(ev)
	}
}

// Stream 实现 Provider。
func (g *Group) Stream(ctx context.Context, req Request, onDelta func(Delta)) error {
	ci := CallFrom(ctx)
	order := g.order(ci.SessionID)
	tried := map[string]bool{}
	var attempts []Attempt
	var lastErr error
	minWindow := 0

	m := g.pick(order, tried, 0, true)
	for try := 1; m != nil; try++ {
		tried[m.key] = true

		actx, cancel := context.WithCancel(ctx)
		var fbFired bool
		var fbMu sync.Mutex
		var timer *time.Timer
		if g.firstByte > 0 {
			timer = time.AfterFunc(g.firstByte, func() {
				fbMu.Lock()
				fbFired = true
				fbMu.Unlock()
				cancel()
			})
		}

		g.mu.Lock()
		sameAsLast := g.lastServed == m.key
		g.mu.Unlock()
		areq := req
		areq.Model = m.Model
		if !sameAsLast {
			areq.Messages = stripThinking(req.Messages)
		}

		emitted := false
		ev, err := observe(actx, UsageEvent{Profile: m.Profile, Model: m.Model, Group: g.cfg.Name, TryNo: try},
			func(on func(Delta)) error { return m.leaf.Stream(actx, areq, on) },
			func(d Delta) {
				if !emitted {
					emitted = true
					if timer != nil {
						timer.Stop()
					}
					onDelta(Delta{Type: DeltaRoute, Route: &Route{Group: g.cfg.Name, Profile: m.Profile, Model: m.Model, Try: try, HandedOn: attempts}})
				}
				onDelta(d)
			})
		if timer != nil {
			timer.Stop()
		}
		cancel()
		fbMu.Lock()
		timedOut := fbFired && !emitted && ctx.Err() == nil
		fbMu.Unlock()
		if timedOut {
			err = fmt.Errorf("%w（%v）", ErrFirstByteTimeout, g.firstByte)
			ev.Status, ev.ErrorKind = StatusError, KindNetwork
		}

		// 成功。
		if err == nil {
			if !emitted {
				onDelta(Delta{Type: DeltaRoute, Route: &Route{Group: g.cfg.Name, Profile: m.Profile, Model: m.Model, Try: try, HandedOn: attempts}})
			}
			g.health.Success(m.key)
			g.mu.Lock()
			g.lastServed = m.key
			if g.cfg.Sticky && ci.SessionID != "" {
				if g.sticky == nil || len(g.sticky) > 256 {
					g.sticky = map[string]string{}
				}
				g.sticky[ci.SessionID] = m.key
			}
			g.mu.Unlock()
			g.record(ev, m.cacheSeparate)
			return nil
		}

		// 用户取消：不是成员的问题。
		if ctx.Err() != nil {
			g.health.release(m.key)
			g.record(ev, m.cacheSeparate)
			return err
		}

		kind := Classify(err)
		lastErr = err
		// 已经输出过内容，或者这类错误换成员也没用：直接报错，不动成员的健康状态。
		if emitted || !kind.Failover() {
			g.health.release(m.key)
			g.record(ev, m.cacheSeparate)
			return err
		}

		var retryAfter time.Duration
		var he *HTTPError
		if errors.As(err, &he) {
			retryAfter = he.RetryAfter
		}
		g.health.Fail(m.key, kind, retryAfter, err.Error())
		attempts = append(attempts, Attempt{Profile: m.Profile, Model: m.Model, Kind: kind, Message: clipMsg(err.Error())})
		if kind == KindContext && m.ctxWindow > minWindow {
			minWindow = m.ctxWindow // 下一个成员必须能装下更多
		}

		// 还有可以试的成员吗？有就把这次记为"已转交"，没有就记为失败。
		// 先选好下一个再记账，这样"已转交"一定对应一次真实发生的下一次尝试。
		next := g.pick(order, tried, minWindow, false)
		if next != nil {
			ev.Status = StatusHandedOn
		}
		g.record(ev, m.cacheSeparate)
		m = next
	}

	if len(attempts) == 0 {
		return &AllFailedError{Group: g.cfg.Name, Skipped: g.skippedNotes(order)}
	}
	return &AllFailedError{Group: g.cfg.Name, Attempts: attempts, Skipped: g.skippedNotes(order, tried), last: lastErr}
}

// skippedNotes 说明没有尝试的成员为什么没有尝试，让"全员不可用"的报错有信息量。
func (g *Group) skippedNotes(order []*groupMember, tried ...map[string]bool) []string {
	var skip map[string]bool
	if len(tried) > 0 {
		skip = tried[0]
	}
	var out []string
	for _, m := range order {
		if skip[m.key] {
			continue
		}
		if m.unavailable != "" {
			out = append(out, fmt.Sprintf("%s / %s 不可用：%s", m.Profile, m.Model, m.unavailable))
			continue
		}
		st := g.health.Status(m.Profile, m.Model)
		switch st.State {
		case StateCooling:
			out = append(out, fmt.Sprintf("%s / %s 冷却中（还需约 %d 秒）：%s", m.Profile, m.Model, max(0, (st.Until-now().UnixMilli())/1000), clipMsg(st.Message)))
		case StateDisabled:
			out = append(out, fmt.Sprintf("%s / %s 已停用（认证失败）：%s", m.Profile, m.Model, clipMsg(st.Message)))
		}
	}
	return out
}

func clipMsg(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return string(r)
}

// GroupStatus 是模型组的运行状态，给设置页显示。
type GroupStatus struct {
	Name     string         `json:"name"`
	Strategy string         `json:"strategy"`
	Members  []MemberStatus `json:"members"`
}

// Status 返回各成员的状态。
func (g *Group) Status() GroupStatus {
	st := GroupStatus{Name: g.cfg.Name, Strategy: g.cfg.Strategy}
	for _, m := range g.members {
		ms := g.health.Status(m.Profile, m.Model)
		ms.Unavailable = m.unavailable
		st.Members = append(st.Members, ms)
	}
	return st
}
