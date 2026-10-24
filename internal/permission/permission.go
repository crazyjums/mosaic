package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Risk 是工具调用的风险等级。等级是"工具类型"层面的粗粒度声明，
// 更细的规则（比如允许 `go test` 但拒绝 `rm`）以后可以在 Policy 实现里根据 Input 判断。
type Risk string

const (
	RiskRead  Risk = "read"  // 只读：读文件、列目录
	RiskWrite Risk = "write" // 修改工作区：写文件
	RiskExec  Risk = "exec"  // 执行任意命令：影响面最大
)

// Verdict 是策略的裁决。
type Verdict string

const (
	Allow Verdict = "allow"
	Ask   Verdict = "ask"
	Deny  Verdict = "deny"
)

// Decision 是用户对一次确认请求的回答。
type Decision string

const (
	DecisionAllow        Decision = "allow"         // 仅本次允许
	DecisionAllowSession Decision = "allow_session" // 本会话内该工具都允许
	DecisionDeny         Decision = "deny"
)

// Valid 检查前端传来的字符串是否是合法决定，避免拼写错误被当成"拒绝"静默处理。
func (d Decision) Valid() bool {
	switch d {
	case DecisionAllow, DecisionAllowSession, DecisionDeny:
		return true
	}
	return false
}

// Allowed 表示这个决定是否放行。
func (d Decision) Allowed() bool { return d == DecisionAllow || d == DecisionAllowSession }

// Policy 决定一次工具调用是否需要用户确认。
//
// 设计成接口是为了以后可以替换为更细粒度的规则引擎（按路径白名单、按命令前缀等），
// 内核只依赖这两个方法。
type Policy interface {
	// Evaluate 给出裁决。input 是工具参数，当前实现不看它，留给更细粒度的策略使用。
	Evaluate(tool string, risk Risk, input json.RawMessage) Verdict
	// Remember 记录用户的决定，例如 allow_session 之后同一工具不再询问。
	Remember(tool string, d Decision)
	// Reset 在切换会话时清空记忆。
	Reset()
}

// Mode 是内置策略的模式。
type Mode string

const (
	// ModeDefault：读操作直接放行，写和执行需要确认。这是推荐的日常模式。
	ModeDefault Mode = "default"
	// ModeAuto：全部放行，不再询问。仅适合在一次性沙箱目录里使用。
	ModeAuto Mode = "auto"
	// ModeReadOnly：只允许读操作，写和执行直接拒绝。适合"只让它看看代码"。
	ModeReadOnly Mode = "readonly"
)

// ModePolicy 是基于 Mode 的内置策略实现。
type ModePolicy struct {
	mode Mode

	mu           sync.Mutex
	sessionAllow map[string]bool
}

// NewModePolicy 创建策略；未知模式按 ModeDefault 处理，宁可多问一次也不要误放行。
func NewModePolicy(mode Mode) *ModePolicy {
	switch mode {
	case ModeDefault, ModeAuto, ModeReadOnly:
	default:
		mode = ModeDefault
	}
	return &ModePolicy{mode: mode, sessionAllow: map[string]bool{}}
}

// ModeSwitcher 是可选能力：支持在运行时切换模式的策略实现它（界面上点击状态栏切换权限模式）。
// 用可选接口而不是加进 Policy：以后基于规则的策略未必有"模式"这个概念。
type ModeSwitcher interface {
	Mode() Mode
	SetMode(Mode) error
}

// Mode 返回当前模式。
func (p *ModePolicy) Mode() Mode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mode
}

// SetMode 切换模式。与 NewModePolicy 不同，这里对未知模式报错——
// 运行时的切换来自界面操作，拼错了应该让调用方知道，而不是悄悄回落。
func (p *ModePolicy) SetMode(m Mode) error {
	switch m {
	case ModeDefault, ModeAuto, ModeReadOnly:
	default:
		return fmt.Errorf("permission: unknown mode %q", m)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = m
	return nil
}

func (p *ModePolicy) Evaluate(tool string, risk Risk, _ json.RawMessage) Verdict {
	if risk == RiskRead {
		return Allow
	}
	switch p.Mode() {
	case ModeAuto:
		return Allow
	case ModeReadOnly:
		return Deny
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessionAllow[tool] {
		return Allow
	}
	return Ask
}

func (p *ModePolicy) Remember(tool string, d Decision) {
	if d != DecisionAllowSession {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessionAllow[tool] = true
}

func (p *ModePolicy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessionAllow = map[string]bool{}
}

// Request 是一次待确认的工具调用。
type Request struct {
	ID      string
	CallID  string
	Tool    string
	Risk    Risk
	Summary string
	Input   json.RawMessage
}

// ErrUnknownRequest 表示回复了一个不存在（已处理/已取消/拼错）的请求。
var ErrUnknownRequest = errors.New("permission: unknown or already resolved request")

// Broker 管理"正在等待用户回答"的请求。
//
// 它解决的是一个跨 goroutine 的会合问题：agent 循环的 goroutine 要停下来等，
// 用户的回答从另一个 goroutine（Wails 的方法调用 / CLI 读 stdin）到来。
// 每个请求一个容量为 1 的 channel：Resolve 往里放决定，Request 从里面取。
type Broker struct {
	mu      sync.Mutex
	pending map[string]chan Decision
}

func NewBroker() *Broker {
	return &Broker{pending: map[string]chan Decision{}}
}

// Request 登记请求 → 通知界面 → 阻塞等待回答或取消。
//
// announce 在登记之后、等待之前被调用（通常用来发 permission_request 事件）。
// 顺序很重要：如果先通知再登记，一个很快的界面（比如测试或自动应答的 CLI）可能在登记之前就调用了
// Resolve，结果拿到 ErrUnknownRequest，agent 永远等不到回答。
//
// ctx 被取消（用户点了"停止"）时返回 ctx.Err()，调用方应把它当作拒绝处理。
func (b *Broker) Request(ctx context.Context, req Request, announce func(Request)) (Decision, error) {
	if req.ID == "" {
		return DecisionDeny, fmt.Errorf("permission: request id is empty")
	}

	// 步骤 1：登记。channel 容量为 1，保证 Resolve 永远不会阻塞。
	ch := make(chan Decision, 1)
	b.mu.Lock()
	b.pending[req.ID] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, req.ID)
		b.mu.Unlock()
	}()

	// 步骤 2：通知界面。注意 announce 可能同步地调用 Resolve（CLI 就是这样做的），
	// 因为 channel 有缓冲，这不会死锁。
	if announce != nil {
		announce(req)
	}

	// 步骤 3：等待回答或取消。
	select {
	case d := <-ch:
		return d, nil
	case <-ctx.Done():
		return DecisionDeny, ctx.Err()
	}
}

// Resolve 回复一个请求。对同一请求重复回复会返回 ErrUnknownRequest。
func (b *Broker) Resolve(id string, d Decision) error {
	if !d.Valid() {
		return fmt.Errorf("permission: invalid decision %q", d)
	}
	b.mu.Lock()
	ch, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
	}
	b.mu.Unlock()
	if !ok {
		return ErrUnknownRequest
	}
	ch <- d
	return nil
}

// PendingIDs 返回当前等待中的请求 ID，用于界面重连后恢复弹窗、以及测试。
func (b *Broker) PendingIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]string, 0, len(b.pending))
	for id := range b.pending {
		ids = append(ids, id)
	}
	return ids
}
