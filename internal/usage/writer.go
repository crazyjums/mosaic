package usage

import (
	"sync"
	"sync/atomic"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// Sink 是账本的写入端（store 实现）。
type Sink interface {
	AppendUsage(ev provider.UsageEvent) error
}

// Writer 异步写入账本，实现 provider.UsageRecorder。
//
// 记账发生在每次模型调用的收尾处，不能因为数据库忙（例如正在保存一个很长的会话）而拖住对话，
// 所以先放进缓冲队列，由后台 goroutine 写入。队列满了（数据库长时间不可用）就丢弃并计数：
// 丢几条统计，远好过卡住对话。
type Writer struct {
	sink    Sink
	ch      chan provider.UsageEvent
	wg      sync.WaitGroup
	dropped atomic.Int64
	onErr   func(error)
	once    sync.Once
}

var _ provider.UsageRecorder = (*Writer)(nil)

// NewWriter 创建并启动写入协程。onErr 可以为 nil；它在写入协程里被调用。
func NewWriter(sink Sink, onErr func(error)) *Writer {
	w := &Writer{sink: sink, ch: make(chan provider.UsageEvent, 512), onErr: onErr}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for ev := range w.ch {
			if err := sink.AppendUsage(ev); err != nil && w.onErr != nil {
				w.onErr(err)
			}
		}
	}()
	return w
}

// RecordUsage 实现 provider.UsageRecorder，不会阻塞。
func (w *Writer) RecordUsage(ev provider.UsageEvent) {
	// Close 之后（应用正在退出）还有收尾的调用来记账：丢弃，而不是向已关闭的 channel 发送而 panic。
	defer func() { _ = recover() }()
	select {
	case w.ch <- ev:
	default:
		w.dropped.Add(1)
	}
}

// Dropped 返回因队列满而丢弃的记录数。
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

// Close 等待队列里的记录写完。之后不能再调用 RecordUsage。
func (w *Writer) Close() {
	w.once.Do(func() { close(w.ch) })
	w.wg.Wait()
}
