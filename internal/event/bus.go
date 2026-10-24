package event

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Publisher 是内核发布事件所需的最小能力。内核依赖这个接口而不是 *Bus，
// 测试里可以换成任何实现（例如只收集事件的切片）。
type Publisher interface {
	Publish(Event) Event
}

// Handler 是订阅者回调。它在发布者的 goroutine 里被同步调用。
type Handler func(Event)

// Bus 是进程内的事件总线：一个发布者（内核）、多个订阅者（界面、日志、测试）。
//
// 设计取舍：同步回调 vs. 每个订阅者一个 channel。
//   - channel 方案能隔离慢订阅者，但要么丢事件（非阻塞发送），要么照样阻塞（阻塞发送），
//     还要处理关闭、缓冲区大小、goroutine 泄漏，复杂度明显更高。
//   - 同步回调保证"所有订阅者看到的顺序 = 发布顺序"，代码最直观。
//     代价是订阅者必须很快返回；需要耗时处理的订阅者应自己起 goroutine 或缓冲。
//
// 目前的订阅者（Wails EventsEmit、终端打印、测试记录）都很快，所以选同步回调。
//
// 约束：Handler 内不要再同步调用 Publish，否则会死锁（发布过程持有锁以保证顺序）。
type Bus struct {
	mu       sync.Mutex
	seq      uint64
	nextID   int
	handlers map[int]Handler
	order    []int // 订阅顺序，保证回调顺序稳定
	now      func() time.Time
}

func NewBus() *Bus {
	return &Bus{handlers: map[int]Handler{}, now: time.Now}
}

// Subscribe 注册一个订阅者，返回取消订阅的函数。
func (b *Bus) Subscribe(h Handler) (unsubscribe func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	b.handlers[id] = h
	b.order = append(b.order, id)
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.handlers, id)
		for i, v := range b.order {
			if v == id {
				b.order = append(b.order[:i], b.order[i+1:]...)
				break
			}
		}
	}
}

// Publish 补全信封（ID、序号、时间、版本、默认 agent），然后依次通知订阅者。
// 返回补全后的事件，方便调用方记录。
func (b *Bus) Publish(ev Event) Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.seq++
	ev.Seq = b.seq
	if ev.ID == "" {
		ev.ID = NewID("ev")
	}
	if ev.Time.IsZero() {
		ev.Time = b.now()
	}
	if ev.Version == 0 {
		ev.Version = ProtocolVersion
	}
	if ev.AgentID == "" {
		ev.AgentID = RootAgentID
	}
	for _, id := range b.order {
		b.handlers[id](ev)
	}
	return ev
}

// NewID 生成带前缀的随机 ID，例如 "msg_3f2a9c..."。前缀让日志里一眼能看出 ID 指的是什么。
// 选择随机 ID 而非自增 ID，是因为会话会被持久化和恢复，自增计数器在重启后会冲突。
func NewID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

// Recorder 是一个收集所有事件的订阅者，用于测试和 CLI 的 --json 模式之外的调试。
type Recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *Recorder) Handle(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

// Events 返回已收集事件的副本。
func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

// Types 返回已收集事件的类型序列，测试里用来断言流程顺序。
func (r *Recorder) Types() []Type {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Type, len(r.events))
	for i, ev := range r.events {
		out[i] = ev.Type
	}
	return out
}
