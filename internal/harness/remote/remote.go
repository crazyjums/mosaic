package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/harness"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// Host 是桌面壳的一侧：真正运行引擎的地方。
type Host interface {
	// RunHarness 请壳运行一轮，阻塞到结束。过程中壳会通过 Hub.Emit / Hub.Ask 回调。
	RunHarness(ctx context.Context, runID string, spec Spec) (Outcome, error)
	// CancelHarness 请壳中断这一轮；壳会尽快让 RunHarness 返回已有的结果。
	CancelHarness(runID string)
}

// Spec 是一轮的输入，原样交给壳里的引擎。
type Spec struct {
	Engine         string         `json:"engine"`
	SessionID      string         `json:"session_id"`
	Profile        string         `json:"profile,omitempty"`
	Text           string         `json:"text"`
	Workspace      string         `json:"workspace"`
	Model          string         `json:"model,omitempty"`
	PermissionMode string         `json:"permission_mode"`
	Options        map[string]any `json:"options,omitempty"`
	Resume         string         `json:"resume,omitempty"`
}

// Outcome 是一轮的结果。
type Outcome struct {
	// Reason：completed / cancelled / error。
	Reason   string             `json:"reason"`
	Error    string             `json:"error,omitempty"`
	Resume   string             `json:"resume,omitempty"`
	Usage    provider.Usage     `json:"usage"`
	Messages []provider.Message `json:"messages,omitempty"`
}

// cancelGrace：取消后给壳整理结果的时间，超过就不再等。
const cancelGrace = 10 * time.Second

// Hub 登记正在进行的轮，把壳回调的事件和审批派发到对应的轮；一个 Hub 服务这个壳上的所有远程引擎。
type Hub struct {
	host Host

	mu   sync.Mutex
	runs map[string]*run
}

type run struct {
	// ctx 是这一轮的 ctx（用户点停止时取消）：等待中的审批要跟着取消，否则停止之后界面上还留着一个没人等的弹窗。
	ctx  context.Context
	emit func(messageID string, p event.Payload)
	ask  func(ctx context.Context, r permission.Request) (permission.Decision, error)
}

func NewHub(host Host) *Hub { return &Hub{host: host, runs: map[string]*run{}} }

// Harness 返回名为 engine 的引擎（对应厂商配置档的 type）。
func (h *Hub) Harness(engine string) harness.Harness { return &remote{hub: h, engine: engine} }

type remote struct {
	hub    *Hub
	engine string
}

func (r *remote) Name() string { return r.engine }

func (r *remote) Run(ctx context.Context, req harness.Request) harness.Result {
	h := r.hub
	runID := event.NewID("hrun")
	h.mu.Lock()
	h.runs[runID] = &run{ctx: ctx, emit: req.Emit, ask: req.Ask}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.runs, runID)
		h.mu.Unlock()
	}()

	spec := Spec{
		Engine: r.engine, SessionID: req.SessionID, Profile: req.Profile, Text: req.Text, Workspace: req.Workspace, Model: req.Model,
		PermissionMode: string(req.PermissionMode), Options: req.Options, Resume: req.Resume,
	}

	// 用户点停止：通知壳中断，再给它一点时间交还已有的结果；超时就不等了。
	runCtx, abort := context.WithCancel(context.Background())
	defer abort()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			h.host.CancelHarness(runID)
			select {
			case <-done:
			case <-time.After(cancelGrace):
				abort()
			}
		case <-done:
		}
	}()

	out, err := h.host.RunHarness(runCtx, runID, spec)
	res := harness.Result{Resume: out.Resume, Usage: out.Usage, Messages: out.Messages}
	if res.Resume == "" {
		res.Resume = req.Resume
	}
	switch {
	case err != nil:
		res.Reason, res.Err = event.TurnError, err
		if ctx.Err() != nil {
			res.Reason, res.Err = event.TurnCancelled, nil
		}
	case out.Reason == "cancelled":
		res.Reason = event.TurnCancelled
	case out.Reason == "error":
		res.Reason, res.Err = event.TurnError, errors.New(nonEmpty(out.Error, "引擎报告这一轮失败了"))
	default:
		res.Reason = event.TurnCompleted
	}
	return res
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// allowedEvents 是引擎有权发的事件。轮的开始/结束、用户消息、权限事件、会话更新由内核掌握，引擎不能伪造。
var allowedEvents = map[event.Type]bool{
	event.TypeMessageStart: true, event.TypeTextDelta: true, event.TypeThinkingDelta: true, event.TypeMessageEnd: true,
	event.TypeToolCall: true, event.TypeToolResult: true, event.TypeError: true, event.TypeImage: true,
}

// Emit 是壳回调：引擎产生了一个事件。
func (h *Hub) Emit(runID, messageID string, typ event.Type, data json.RawMessage) error {
	if !allowedEvents[typ] {
		return fmt.Errorf("引擎不能发 %q 事件", typ)
	}
	h.mu.Lock()
	r := h.runs[runID]
	h.mu.Unlock()
	if r == nil {
		return fmt.Errorf("没有进行中的轮 %q", runID)
	}
	body, err := json.Marshal(map[string]any{"type": typ, "data": json.RawMessage(data)})
	if err != nil {
		return err
	}
	ev, err := event.Decode(body)
	if err != nil {
		return err
	}
	r.emit(messageID, ev.Data)
	return nil
}

// AskRequest 是引擎要用户确认的操作。
type AskRequest struct {
	CallID    string          `json:"call_id"`
	Tool      string          `json:"tool"`
	Risk      string          `json:"risk"`
	Sensitive bool            `json:"sensitive,omitempty"`
	Summary   string          `json:"summary"`
	Input     json.RawMessage `json:"input,omitempty"`
}

// Ask 是壳回调：引擎要用户确认。阻塞到用户回答或 ctx 取消；返回 allow / allow_session / deny。
func (h *Hub) Ask(ctx context.Context, runID string, a AskRequest) (string, error) {
	h.mu.Lock()
	r := h.runs[runID]
	h.mu.Unlock()
	if r == nil {
		return string(permission.DecisionDeny), fmt.Errorf("没有进行中的轮 %q", runID)
	}
	risk := permission.Risk(a.Risk)
	switch risk {
	case permission.RiskRead, permission.RiskWrite, permission.RiskExec:
	default:
		risk = permission.RiskExec // 不认识的风险级别按最高处理
	}
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	d, err := r.ask(actx, permission.Request{ID: event.NewID("perm"), CallID: a.CallID, Tool: a.Tool, Risk: risk, Sensitive: a.Sensitive, Summary: a.Summary, Input: a.Input})
	return string(d), err
}
