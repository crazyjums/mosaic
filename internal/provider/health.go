package provider

import (
	"sync"
	"time"
)

// 模型组成员的健康状态。
//
//	健康 ──失败──▶ 冷却中 ──到期──▶ 试探中 ──成功──▶ 健康
//	                 ▲                 │
//	                 └────再次失败─────┘（冷却时间翻倍）
//	健康 ──认证失败──▶ 已停用（直到重新登录 / 修改配置 / 手动恢复）
//
// 状态只放内存：冷却是为了避免反复撞墙，应用重启后全部重置为健康，重新试一次的代价很小。
// 冷却的时长是本项目自己的设计，没有照搬任何现成实现，数值见下面的常量。

// MemberState 是成员当前的状态。
type MemberState string

const (
	StateHealthy  MemberState = "healthy"
	StateCooling  MemberState = "cooling"
	StateProbing  MemberState = "probing"
	StateDisabled MemberState = "disabled"
)

const (
	baseCooldown      = 30 * time.Second
	maxCooldown       = 10 * time.Minute
	defaultRateLimit  = 60 * time.Second
	defaultQuotaWait  = 30 * time.Minute
	maxRetryAfterWait = time.Hour // 厂商要求等多久就等多久，但不超过一小时
)

// MemberStatus 是给界面显示的成员状态。
type MemberStatus struct {
	Profile string      `json:"profile"`
	Model   string      `json:"model"`
	State   MemberState `json:"state"`
	// Until 是冷却到期时间（Unix 毫秒）；非冷却状态为 0。
	Until int64 `json:"until,omitempty"`
	// Kind / Message 是最近一次失败的原因。
	Kind    ErrorKind `json:"kind,omitempty"`
	Message string    `json:"message,omitempty"`
	Fails   int       `json:"fails,omitempty"`
	// Unavailable 说明成员本身不可用（例如引用的配置档被删了、缺密钥），与运行时的健康状态无关。
	Unavailable string `json:"unavailable,omitempty"`
}

type memberHealth struct {
	fails    int
	until    time.Time
	kind     ErrorKind
	message  string
	probing  bool
	disabled bool
}

// Health 保存所有模型组成员的健康状态，在厂商目录重建（保存设置）之间保持，所以由 Manager 持有并传入。
type Health struct {
	mu  sync.Mutex
	m   map[string]*memberHealth
	now func() time.Time
}

func NewHealth() *Health { return &Health{m: map[string]*memberHealth{}, now: time.Now} }

// MemberKey 是成员在健康表里的键。
func MemberKey(profile, model string) string { return profile + "/" + model }

func (h *Health) get(key string) *memberHealth {
	mh := h.m[key]
	if mh == nil {
		mh = &memberHealth{}
		h.m[key] = mh
	}
	return mh
}

// acquire 判断成员此刻能不能用，能用时占用它：
// 健康的直接用；冷却已到期的放行一个请求去试探（同一时间只有一个）；仍在冷却、正在试探、已停用的不能用。
func (h *Health) acquire(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	mh := h.m[key]
	if mh == nil {
		return true
	}
	switch {
	case mh.disabled || mh.probing:
		return false
	case mh.until.IsZero():
		return true
	case !h.now().Before(mh.until):
		mh.probing = true
		return true
	}
	return false
}

// force 在所有成员都不可用时，强制占用冷却时间最早到期的那个（已停用的除外），让请求至少有一次机会。
func (h *Health) force(keys []string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	best, bestUntil := "", time.Time{}
	for _, k := range keys {
		mh := h.m[k]
		if mh == nil {
			best = k
			break
		}
		if mh.disabled || mh.probing {
			continue
		}
		if best == "" || mh.until.Before(bestUntil) {
			best, bestUntil = k, mh.until
		}
	}
	if best == "" {
		return "", false
	}
	h.get(best).probing = true
	return best, true
}

// release 结束一次占用但不下结论（用户取消、与成员无关的错误）。
func (h *Health) release(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if mh := h.m[key]; mh != nil {
		mh.probing = false
	}
}

// Success 记录成功：恢复健康。
func (h *Health) Success(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.m, key)
}

// Fail 记录一次失败并决定冷却时间。
//
//	限流   ：厂商给了 Retry-After 就用它，否则 60 秒；
//	额度   ：Retry-After 或 30 分钟；
//	认证   ：停用，不自动试探（令牌刷新已由适配器处理过，仍失败说明密钥有问题）；
//	网络/服务端：30 秒起，连续失败翻倍，上限 10 分钟；
//	上下文超长：不是成员的问题，不冷却。
func (h *Health) Fail(key string, kind ErrorKind, retryAfter time.Duration, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	mh := h.get(key)
	mh.probing = false
	if kind == KindContext {
		return
	}
	mh.fails++
	mh.kind, mh.message = kind, message
	wait := time.Duration(0)
	switch kind {
	case KindAuth:
		mh.disabled = true
		return
	case KindRateLimit:
		wait = defaultRateLimit
	case KindQuota:
		wait = defaultQuotaWait
	default:
		wait = baseCooldown << min(mh.fails-1, 5)
	}
	if retryAfter > 0 {
		wait = min(retryAfter, maxRetryAfterWait)
	} else if wait > maxCooldown && kind != KindQuota {
		wait = maxCooldown
	}
	mh.until = h.now().Add(wait)
}

// Reset 手动恢复一个成员（界面上的"恢复"按钮）。
func (h *Health) Reset(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.m, key)
}

// ResetProfile 恢复一个配置档下的所有成员：用户修改了这个配置档（换了密钥、重新登录）后，之前的失败记录不再成立。
func (h *Health) ResetProfile(profile string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prefix := profile + "/"
	for k := range h.m {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(h.m, k)
		}
	}
}

// Status 返回成员的状态。
func (h *Health) Status(profile, model string) MemberStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := MemberStatus{Profile: profile, Model: model, State: StateHealthy}
	mh := h.m[MemberKey(profile, model)]
	if mh == nil {
		return st
	}
	st.Kind, st.Message, st.Fails = mh.kind, mh.message, mh.fails
	switch {
	case mh.disabled:
		st.State = StateDisabled
	case mh.probing:
		st.State = StateProbing
	case !mh.until.IsZero() && h.now().Before(mh.until):
		st.State, st.Until = StateCooling, mh.until.UnixMilli()
	case !mh.until.IsZero():
		st.State = StateProbing // 冷却已到期，等待下一个请求去试探
	}
	return st
}
