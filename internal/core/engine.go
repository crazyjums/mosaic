package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// EngineConfig 是 Engine 的依赖。由 internal/bootstrap 根据配置文件组装。
type EngineConfig struct {
	Catalog *provider.Catalog
	Tools   *tool.Registry
	// Store 为 nil 时不持久化（测试、一次性 CLI 会话）。
	Store     SessionStore
	Policy    permission.Policy
	Compactor Compactor
	Subagents *SubagentRegistry

	Workspace    string
	SystemPrompt string
	MaxSteps     int
	MaxTokens    int

	DefaultProfile string
	DefaultModel   string
}

// Engine 是内核对外的"命令接口"。
//
// 内核对外只有两个方向：
//   - 命令（本类型的方法）：新建/加载会话、发送消息、取消、回复权限、切换模型……
//   - 事件（Bus）：一切状态变化。
//
// CLI 和 Wails 绑定层都只依赖这两样，所以它们可以非常薄；
// 也正因为如此，内核可以完全脱离 Wails 运行（见 cmd/cli）。
//
// Engine 同一时间只运行一个会话的一轮对话（第一阶段的范围），用 running 标志保护。
type Engine struct {
	cfg    EngineConfig
	bus    *event.Bus
	broker *permission.Broker

	mu      sync.Mutex
	session *Session
	running bool
	cancel  context.CancelFunc
}

// 常见错误，界面可以据此给出友好提示。
var (
	ErrBusy = errors.New("engine: a turn is already running")
)

// NewEngine 创建引擎并开启一个空会话。
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.Catalog == nil {
		return nil, fmt.Errorf("engine: catalog is required")
	}
	if cfg.Tools == nil {
		cfg.Tools = tool.NewRegistry()
	}
	if cfg.Policy == nil {
		cfg.Policy = permission.NewModePolicy(permission.ModeDefault)
	}
	if cfg.Subagents == nil {
		cfg.Subagents = NewSubagentRegistry()
	}
	e := &Engine{cfg: cfg, bus: event.NewBus(), broker: permission.NewBroker()}

	profile, model, err := e.resolveModel(cfg.DefaultProfile, cfg.DefaultModel)
	if err != nil {
		return nil, err
	}
	e.session = NewSession(event.NewID("sess"), profile, model)
	return e, nil
}

// Bus 返回事件总线，界面通过它订阅事件。
func (e *Engine) Bus() *event.Bus { return e.bus }

// resolveModel 校验配置档可用，并在未指定模型时取默认模型。
func (e *Engine) resolveModel(profile, model string) (string, string, error) {
	if profile == "" {
		// 没有指定时，选第一个可用的配置档。
		for _, p := range e.cfg.Catalog.Profiles() {
			if p.Available {
				profile = p.Name
				break
			}
		}
		if profile == "" {
			return "", "", fmt.Errorf("engine: no available provider profile, check your config")
		}
	}
	_, cfg, err := e.cfg.Catalog.Get(profile)
	if err != nil {
		return "", "", err
	}
	if model == "" {
		model = cfg.DefaultModel
	}
	if model == "" && len(cfg.Models) > 0 {
		model = cfg.Models[0]
	}
	if model == "" {
		return "", "", fmt.Errorf("engine: profile %q has no model configured", profile)
	}
	return profile, model, nil
}

// Status 是界面状态栏需要的信息。
type Status struct {
	SessionID string         `json:"session_id"`
	Title     string         `json:"title"`
	Provider  string         `json:"provider"`
	Model     string         `json:"model"`
	Usage     provider.Usage `json:"usage"`
	Running   bool           `json:"running"`
	Workspace string         `json:"workspace"`
	// PendingPermissions 让重新连接的界面知道是否有弹窗需要恢复。
	PendingPermissions []string `json:"pending_permissions"`
}

func (e *Engine) Status() Status {
	e.mu.Lock()
	sess, running := e.session, e.running
	e.mu.Unlock()
	snap := sess.Snapshot()
	return Status{
		SessionID:          snap.ID,
		Title:              snap.Title,
		Provider:           snap.Provider,
		Model:              snap.Model,
		Usage:              snap.Usage,
		Running:            running,
		Workspace:          e.cfg.Workspace,
		PendingPermissions: e.broker.PendingIDs(),
	}
}

// Profiles 返回所有厂商配置档（不含密钥），用于界面上的厂商/模型选择器。
func (e *Engine) Profiles() []provider.ProfileInfo { return e.cfg.Catalog.Profiles() }

// SetModel 切换当前会话使用的厂商和模型。下一次发送消息时生效。
//
// 同一会话中途换厂商是允许的：历史是中立格式，每个适配器发请求时各自转换。
// 唯一的损失是 Anthropic 的思考签名对其他厂商没有意义（OpenAI 适配器会丢弃思考块）。
func (e *Engine) SetModel(profile, model string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return ErrBusy
	}
	p, m, err := e.resolveModel(profile, model)
	if err != nil {
		return err
	}
	e.session.setModel(p, m)
	return nil
}

// NewSession 开启一个新的空会话（沿用当前的厂商和模型）。
func (e *Engine) NewSession() (Status, error) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return Status{}, ErrBusy
	}
	old := e.session.Snapshot()
	e.session = NewSession(event.NewID("sess"), old.Provider, old.Model)
	e.mu.Unlock()
	e.cfg.Policy.Reset()
	return e.Status(), nil
}

// Send 发送一条用户消息并阻塞直到这一轮结束。
//
// 阻塞式 API 对 CLI 和测试最自然；Wails 绑定层会在 goroutine 里调用它，避免卡住界面线程。
// ctx 用于外部取消；Cancel() 也能取消当前这一轮。
func (e *Engine) Send(ctx context.Context, text string) (TurnResult, error) {
	// 步骤 1：占用运行权，准备好本轮要用的依赖。
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return TurnResult{}, ErrBusy
	}
	sess := e.session
	snap := sess.Snapshot()
	p, _, err := e.cfg.Catalog.Get(snap.Provider)
	if err != nil {
		e.mu.Unlock()
		return TurnResult{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	e.running, e.cancel = true, cancel
	e.mu.Unlock()

	defer func() {
		cancel()
		e.mu.Lock()
		e.running, e.cancel = false, nil
		e.mu.Unlock()
	}()

	// 步骤 2：组装一个主 agent 并运行。每轮新建 Agent 很便宜（只是一份配置），
	// 这样切换模型后不需要任何"刷新"逻辑。
	agent := NewAgent(AgentConfig{
		ID:        event.RootAgentID,
		Provider:  p,
		Model:     snap.Model,
		System:    e.cfg.SystemPrompt,
		MaxTokens: e.cfg.MaxTokens,
		MaxSteps:  e.cfg.MaxSteps,
		Tools:     e.cfg.Tools,
		Policy:    e.cfg.Policy,
		Broker:    e.broker,
		Publisher: e.bus,
		Compactor: e.cfg.Compactor,
		Workspace: e.cfg.Workspace,
	})
	res := agent.Run(ctx, sess, text)

	// 步骤 3：持久化。即使本轮出错/取消也要保存——用户已经看到的内容不应该丢。
	// 用 context.Background()：本轮被取消时保存仍需完成。
	if e.cfg.Store != nil {
		if err := e.cfg.Store.SaveSession(context.Background(), sess.Snapshot()); err != nil {
			e.bus.Publish(event.New(sess.ID, event.RootAgentID, "", event.Error{
				Code: "store_error", Message: "保存会话失败: " + err.Error(),
			}))
		}
	}
	return res, nil
}

// Cancel 取消正在运行的一轮（如果有）。正在等待的权限确认会按拒绝处理。
func (e *Engine) Cancel() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
}

// RespondPermission 回复一个权限确认请求。decision 取值 allow / allow_session / deny。
func (e *Engine) RespondPermission(requestID, decision string) error {
	return e.broker.Resolve(requestID, permission.Decision(decision))
}

// ListSessions 列出已保存的会话。
func (e *Engine) ListSessions(ctx context.Context) ([]SessionSummary, error) {
	if e.cfg.Store == nil {
		return nil, nil
	}
	return e.cfg.Store.ListSessions(ctx)
}

// LoadSession 加载一个历史会话作为当前会话，并把历史重放成事件发到总线上。
func (e *Engine) LoadSession(ctx context.Context, id string) (Status, error) {
	if e.cfg.Store == nil {
		return Status{}, fmt.Errorf("engine: no session store configured")
	}
	s, err := e.cfg.Store.LoadSession(ctx, id)
	if err != nil {
		return Status{}, err
	}
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return Status{}, ErrBusy
	}
	// 会话里记录的厂商配置档可能已经被删掉了；此时回落到默认配置档，历史仍然可读。
	if _, _, err := e.cfg.Catalog.Get(s.Provider); err != nil {
		if p, m, err2 := e.resolveModel("", ""); err2 == nil {
			s.Provider, s.Model = p, m
		}
	}
	e.session = s
	e.mu.Unlock()
	e.cfg.Policy.Reset()

	for _, ev := range ReplayEvents(s) {
		e.bus.Publish(ev)
	}
	return e.Status(), nil
}

// DeleteSession 删除一个已保存的会话。不能删除当前会话之外的正在运行的会话（只有一个），所以无需额外检查。
func (e *Engine) DeleteSession(ctx context.Context, id string) error {
	if e.cfg.Store == nil {
		return nil
	}
	return e.cfg.Store.DeleteSession(ctx, id)
}

// Subagents 返回子 agent 定义注册表（预留扩展点）。
func (e *Engine) Subagents() *SubagentRegistry { return e.cfg.Subagents }
