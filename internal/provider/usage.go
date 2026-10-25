package provider

import (
	"context"
	"errors"
	"time"
)

// 用量计量：每次模型调用（含故障转移里的每一次尝试）记一条 UsageEvent。
//
// 为什么放在 Provider 这一层而不是 core 的 callModel：标题生成、将来的子 agent 都直接调用 Provider，
// 只有在所有调用的唯一出口记账，账才对得上；而且故障转移时要记的是"实际应答的成员"，只有这里知道。

// 调用状态。
const (
	StatusOK        = "ok"
	StatusError     = "error"
	StatusCancelled = "cancelled"
	StatusHandedOn  = "handed_on" // 失败后已转交给模型组里的下一个成员
)

// 调用来源。
const (
	SourceChat  = "chat"
	SourceTitle = "title"
)

// UsageEvent 是一次调用（尝试）的用量记录。只有数字和名称，不含任何提示词或回答内容。
type UsageEvent struct {
	Time      time.Time
	SessionID string
	Source    string
	Profile   string // 实际应答的厂商配置档
	Model     string
	Group     string // 经哪个模型组路由；直连为空
	TryNo     int    // 第几次尝试，从 1 开始
	Status    string
	ErrorKind ErrorKind
	Usage     Usage
	// DurationMS 是整次调用的耗时；TTFTMS 是首个内容到达的延迟，没有测到为 0。
	DurationMS int64
	TTFTMS     int64
}

// UsageRecorder 接收用量记录。实现必须快速返回、可以并发调用（见 usage.Writer）。
type UsageRecorder interface {
	RecordUsage(UsageEvent)
}

// CallInfo 是调用方通过 context 传给计量层的信息。
type CallInfo struct {
	SessionID string
	Source    string
}

type callKey struct{}

// WithCall 在 ctx 里带上会话 ID 和调用来源，计量层据此记账。没有设置时来源按"对话"处理。
func WithCall(ctx context.Context, ci CallInfo) context.Context {
	return context.WithValue(ctx, callKey{}, ci)
}

// CallFrom 取出 WithCall 设置的信息。
func CallFrom(ctx context.Context) CallInfo {
	ci, _ := ctx.Value(callKey{}).(CallInfo)
	if ci.Source == "" {
		ci.Source = SourceChat
	}
	return ci
}

// now 可以在测试里替换。
var now = time.Now

// observe 执行一次流式调用并记录用量、耗时和首字延迟，返回填好的 UsageEvent（不负责写入）。
// base 里预先填好 Profile / Model / Group / TryNo 等字段。
//
// Route 增量不算"内容"：它只是模型组告诉上层"实际是谁在应答"，不能用来计算首字延迟。
func observe(ctx context.Context, base UsageEvent, stream func(onDelta func(Delta)) error, onDelta func(Delta)) (UsageEvent, error) {
	ci := CallFrom(ctx)
	ev := base
	ev.SessionID, ev.Source = ci.SessionID, ci.Source
	start := now()
	ev.Time = start
	var first time.Time
	err := stream(func(d Delta) {
		if d.Type == DeltaUsage && d.Usage != nil {
			ev.Usage = *d.Usage // 以最后一次为准（累计值）
		}
		if first.IsZero() && d.Type != DeltaRoute && d.Type != DeltaUsage {
			first = now()
		}
		onDelta(d)
	})
	ev.DurationMS = now().Sub(start).Milliseconds()
	if !first.IsZero() {
		ev.TTFTMS = first.Sub(start).Milliseconds()
	}
	switch {
	case err == nil:
		ev.Status = StatusOK
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		ev.Status, ev.ErrorKind = StatusCancelled, KindCancelled
	default:
		ev.Status, ev.ErrorKind = StatusError, Classify(err)
	}
	return ev, err
}

// normalizeUsage 统一"输入 token"的口径：账本里的 InputTokens 一律包含缓存读写的部分。
//
// 各家的算法不同：OpenAI 系的 prompt_tokens 已经包含缓存命中的部分（缓存读 ⊂ 输入）；
// Anthropic 的 input_tokens 只算没命中缓存的新输入，缓存读写是另外两个数。
// 不统一的话，同样的"输入"在不同厂商下含义不同，既不能加总也没法算费用。
// 统一之后：费用 = (输入 − 缓存读 − 缓存写) × 输入价 + 缓存读 × 缓存读价 + 缓存写 × 缓存写价 + 输出 × 输出价。
func normalizeUsage(u Usage, cacheSeparate bool) Usage {
	if cacheSeparate {
		u.InputTokens += u.CacheReadTokens + u.CacheWriteTokens
	}
	return u
}

// cacheSeparateType 说明某个适配器类型的输入 token 是否不含缓存部分。
func cacheSeparateType(adapterType string) bool { return adapterType == "anthropic" }

// metered 给一个 Provider 加上用量记录。每次 Stream 记一条。
type metered struct {
	inner         Provider
	rec           UsageRecorder
	profile       string
	cacheSeparate bool
}

func (m *metered) Name() string { return m.inner.Name() }

func (m *metered) Stream(ctx context.Context, req Request, onDelta func(Delta)) error {
	ev, err := observe(ctx, UsageEvent{Profile: m.profile, Model: req.Model, TryNo: 1}, func(on func(Delta)) error {
		return m.inner.Stream(ctx, req, on)
	}, onDelta)
	ev.Usage = normalizeUsage(ev.Usage, m.cacheSeparate)
	m.rec.RecordUsage(ev)
	return err
}
