package core

import (
	"context"
	"errors"
	"time"

	"github.com/crazyjums/myllmapp/internal/event"
)

// 历史会话的分页加载。
//
// 为什么要分页：一个会话可能有几百轮，原来的做法是把全部历史重放成事件，每个事件一次 IPC，
// 前端再逐条处理并重新渲染——长会话要卡很久。界面其实只需要最近的几轮，更早的按需再取。
//
// 分页只影响"界面显示"：内存里的会话仍然是完整的，下一次发给模型的上下文不受影响。
// 重放的事件不经过事件总线，而是作为调用结果直接返回：一次调用、一批事件，前端折叠完再渲染一次。

// DefaultHistoryTurns 是打开历史会话时默认加载的最近轮数，也是"加载更早的"每次加载的轮数。
const DefaultHistoryTurns = 20

// ErrHistoryChanged 表示会话历史在加载之后被改写（例如压缩了上下文），分页位置已经失效，需要重新打开会话。
var ErrHistoryChanged = errors.New("engine: session history changed, reopen the session")

// histWindow 是界面已经加载的历史范围：消息下标 [from, 末尾)。
type histWindow struct {
	sessionID string
	epoch     int
	from      int
}

// History 是一批重放事件，加上这批之前还有多少轮没有加载。
type History struct {
	Events []event.Event `json:"events"`
	// Earlier 是还没有加载的更早的轮数；0 表示已经到头了。
	Earlier int `json:"earlier"`
}

// Loaded 是 LoadSessionTail 的结果。
type Loaded struct {
	Status  Status `json:"status"`
	History        // 事件字段与 Status 平级
}

// LoadSessionTail 加载历史会话作为当前会话，只返回最近 turns 轮（<=0 用默认值）的重放事件。
// 事件不发布到总线，由调用方直接交给前端。运行中返回 ErrBusy。
func (e *Engine) LoadSessionTail(ctx context.Context, id string, turns int) (Loaded, error) {
	if turns <= 0 {
		turns = DefaultHistoryTurns
	}
	s, err := e.openSession(ctx, id)
	if err != nil {
		return Loaded{}, err
	}
	snap := s.Snapshot()
	starts := turnStarts(snap.Messages)
	first := 0 // 第一个要加载的轮
	if len(starts) > turns {
		first = len(starts) - turns
	}
	from := 0
	if first > 0 {
		from = starts[first]
	}
	e.mu.Lock()
	e.hist = histWindow{sessionID: snap.ID, epoch: snap.epoch, from: from}
	e.mu.Unlock()
	return Loaded{
		Status:  e.Status(),
		History: History{Events: stamp(replayRange(snap, e.riskOf, from, len(snap.Messages))), Earlier: first},
	}, nil
}

// LoadEarlier 返回当前已加载范围之前的 turns 轮（<=0 用默认值），并把已加载范围向前扩。
// 事件按时间顺序排列，调用方把它们放到已有内容的前面。可以在运行中调用。
func (e *Engine) LoadEarlier(sessionID string, turns int) (History, error) {
	if turns <= 0 {
		turns = DefaultHistoryTurns
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil || e.session.ID != sessionID || e.hist.sessionID != sessionID {
		return History{}, ErrHistoryChanged
	}
	snap := e.session.Snapshot()
	if snap.epoch != e.hist.epoch {
		return History{}, ErrHistoryChanged
	}
	starts := turnStarts(snap.Messages)
	loaded := 0 // from 之前有多少轮
	for _, st := range starts {
		if st < e.hist.from {
			loaded++
		}
	}
	if loaded == 0 {
		return History{}, nil
	}
	first := loaded - turns
	if first < 0 {
		first = 0
	}
	from := starts[first]
	evs := stamp(replayRange(snap, e.riskOf, from, e.hist.from))
	e.hist.from = from
	return History{Events: evs, Earlier: first}, nil
}

// stamp 补全信封里本来由总线填写的字段（ID、时间）。序号保持 0：这些事件不在实时事件流里。
func stamp(evs []event.Event) []event.Event {
	now := time.Now()
	for i := range evs {
		evs[i].ID = event.NewID("ev")
		evs[i].Time = now
	}
	return evs
}
