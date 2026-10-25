package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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

	// Workspace 是默认工作区：没有选过工作区的会话使用它。每个会话可以有自己的工作区（Session.Workspace）。
	Workspace string
	// WorkspaceLocked 为 true 时所有会话都使用 Workspace，不能单独设置（CLI 的 -workspace 参数）。
	WorkspaceLocked bool
	// SystemPrompt 为空时使用 DefaultSystemPrompt(会话的工作区)。
	SystemPrompt string
	MaxSteps     int
	MaxTokens    int

	DefaultProfile string
	DefaultModel   string

	// DisabledTools 是被禁用的工具名。
	DisabledTools []string
	// TitleMode 决定会话标题怎么来：first_message（默认，截取第一句）或 model（第一轮结束后让模型生成）。
	TitleMode string
	// SkillsPrompt 返回追加到系统提示词后面的技能清单（参数是会话的工作区，技能随工作区变化）；为 nil 或返回空串表示没有技能。
	// 用回调而不是直接依赖技能包：内核只依赖契约层（见 arch_test.go），技能的发现由装配层提供。
	SkillsPrompt func(workspace string) string
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
	// hist 记录界面已经加载了当前会话历史的哪一段，见 LoadSessionTail。
	hist    histWindow
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
	if errors.Is(err, ErrNoProvider) {
		// 一个厂商都没配置（全新安装）是正常状态：界面会引导用户去添加。
		// 会话先不绑定厂商，发送消息时才提示。
		profile, model, err = "", "", nil
	}
	if err != nil {
		return nil, err
	}
	e.session = NewSession(event.NewID("sess"), profile, model)
	return e, nil
}

// ErrNoProvider 表示还没有任何可用的模型厂商（例如全新安装、还没填 API Key）。
var ErrNoProvider = errors.New("还没有可用的模型厂商：请先在 设置 → 模型厂商 添加一个（填写 Base URL 和 API Key，或登录 ChatGPT 账号；命令行下编辑 config.toml，或加 -demo 体验）")

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
			return "", "", ErrNoProvider
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
	// Workspace 是当前会话实际使用的工作区。
	Workspace string `json:"workspace"`
	// WorkspaceIsDefault 为 true 表示当前会话没有单独选过工作区，跟随默认工作区。
	WorkspaceIsDefault bool `json:"workspace_is_default"`
	// WorkspaceLocked 为 true 表示工作区由启动参数指定，不能为会话单独选择。
	WorkspaceLocked bool `json:"workspace_locked"`
	// WorkspaceMissing 为 true 表示工作区目录不存在（例如被删除或移动了），发送消息前需要重新选择。
	WorkspaceMissing bool `json:"workspace_missing"`
	// PendingPermissions 让重新连接的界面知道是否有弹窗需要恢复。
	PendingPermissions []string `json:"pending_permissions"`
	// PermissionMode 是当前权限档位（readonly / default / accept_edits / auto）；策略不支持切换时为空。
	PermissionMode string `json:"permission_mode"`
}

func (e *Engine) Status() Status {
	e.mu.Lock()
	sess, running := e.session, e.running
	workspace, isDefault := e.workspaceLocked(sess)
	locked := e.cfg.WorkspaceLocked
	e.mu.Unlock()
	snap := sess.Snapshot()
	return Status{
		WorkspaceIsDefault: isDefault,
		WorkspaceLocked:    locked,
		WorkspaceMissing:   workspace != "" && !isDir(workspace),
		SessionID:          snap.ID,
		Title:              snap.Title,
		Provider:           snap.Provider,
		Model:              snap.Model,
		Usage:              snap.Usage,
		Running:            running,
		Workspace:          workspace,
		PendingPermissions: e.broker.PendingIDs(),
		PermissionMode:     e.permissionMode(),
	}
}

func (e *Engine) permissionMode() string {
	if ms, ok := e.cfg.Policy.(permission.ModeSwitcher); ok {
		return string(ms.Mode())
	}
	return ""
}

// SetPermissionMode 切换权限档位（readonly / default / accept_edits / auto），立即生效。
//
// 运行中也允许切换：策略在每次工具调用前才被询问，所以本轮里还没执行的工具调用会按新档位处理。
// 已经弹出、正在等待回答的确认请求不受影响（用户仍需回答它）。
func (e *Engine) SetPermissionMode(mode string) error {
	m, err := permission.ParseMode(mode)
	if err != nil {
		return err
	}
	ms, ok := e.cfg.Policy.(permission.ModeSwitcher)
	if !ok {
		return fmt.Errorf("engine: current permission policy does not support modes")
	}
	return ms.SetMode(m)
}

// Profiles 返回所有厂商配置档（不含密钥），用于界面上的厂商/模型选择器。
func (e *Engine) Profiles() []provider.ProfileInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.Catalog.Profiles()
}

// RuntimeConfig 是可以在运行时修改的参数（设置 → 常规 / 系统提示词）。
type RuntimeConfig struct {
	Workspace string `json:"workspace"`
	// TitleMode：first_message / model，见 EngineConfig.TitleMode。
	TitleMode string `json:"title_mode"`
	// SystemPrompt 为空表示使用 DefaultSystemPrompt(Workspace)。
	SystemPrompt string `json:"system_prompt"`
	MaxSteps     int    `json:"max_steps"`
	MaxTokens    int    `json:"max_tokens"`
}

// Runtime 返回当前的运行参数（SystemPrompt 为空表示正在使用默认提示词）。
func (e *Engine) Runtime() RuntimeConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return RuntimeConfig{Workspace: e.cfg.Workspace, SystemPrompt: e.cfg.SystemPrompt, MaxSteps: e.cfg.MaxSteps, MaxTokens: e.cfg.MaxTokens, TitleMode: e.cfg.TitleMode}
}

// workspaceLocked 返回会话实际使用的工作区，以及它是不是默认工作区。调用方需持有 e.mu。
func (e *Engine) workspaceLocked(s *Session) (dir string, isDefault bool) {
	if ws := s.workspace(); ws != "" && !e.cfg.WorkspaceLocked {
		return ws, false
	}
	return e.cfg.Workspace, true
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// runtimeLocked 返回本轮实际使用的参数：工作区取当前会话的，补上默认值、展开默认提示词。调用方需持有 e.mu。
func (e *Engine) runtimeLocked() RuntimeConfig {
	ws, _ := e.workspaceLocked(e.session)
	rt := RuntimeConfig{Workspace: ws, SystemPrompt: e.cfg.SystemPrompt, MaxSteps: e.cfg.MaxSteps, MaxTokens: e.cfg.MaxTokens, TitleMode: e.cfg.TitleMode}
	if rt.SystemPrompt == "" {
		rt.SystemPrompt = DefaultSystemPrompt(rt.Workspace)
	}
	rt.SystemPrompt += ProjectContext(rt.Workspace)
	if e.cfg.SkillsPrompt != nil {
		rt.SystemPrompt += e.cfg.SkillsPrompt(rt.Workspace)
	}
	return rt
}

// UpdateRuntime 修改运行参数（Workspace 是默认工作区），从下一轮开始生效。运行中返回 ErrBusy：
// 一轮中途换工作区，前后两次工具调用会落在不同目录，结果难以理解。
// 调用方负责校验（例如工作区目录存在），内核只做替换。
func (e *Engine) UpdateRuntime(rt RuntimeConfig) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return ErrBusy
	}
	if rt.Workspace == "" {
		return fmt.Errorf("engine: workspace is required")
	}
	// 换了工作区，之前"本会话总是允许"的授权是针对旧目录给的，不应该带到新目录。
	// （只有当前会话跟随默认工作区时，它的工作区才真的变了。）
	if _, isDefault := e.workspaceLocked(e.session); rt.Workspace != e.cfg.Workspace && isDefault {
		e.cfg.Policy.Reset()
	}
	e.cfg.Workspace, e.cfg.SystemPrompt, e.cfg.MaxSteps, e.cfg.MaxTokens = rt.Workspace, rt.SystemPrompt, rt.MaxSteps, rt.MaxTokens
	e.cfg.TitleMode = rt.TitleMode
	return nil
}

// ReplaceCatalog 用新的厂商目录替换旧的，用于界面上修改厂商配置后立即生效，不需要重启。
//
// 当前会话使用的配置档如果在新目录里不存在了（被删除、改名）或变得不可用，
// 回落到第一个可用的配置档。运行中不允许替换：正在进行的请求还持有旧的 Provider。
func (e *Engine) ReplaceCatalog(c *provider.Catalog) error {
	if c == nil {
		return fmt.Errorf("engine: catalog is nil")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return ErrBusy
	}
	// 回落失败（新目录里没有可用的配置档）时恢复原目录，引擎保持原状，
	// 不会出现"目录换了、会话还指向已不存在的配置档"的半更新状态。
	old := e.cfg.Catalog
	e.cfg.Catalog = c
	snap := e.session.Snapshot()
	if _, _, err := c.Get(snap.Provider); err != nil {
		p, m, err := e.resolveModel("", "")
		if errors.Is(err, ErrNoProvider) {
			p, m, err = "", "", nil // 删光了所有厂商：会话回到"未配置"状态
		}
		if err != nil {
			e.cfg.Catalog = old
			return err
		}
		e.session.setModel(p, m)
	}
	return nil
}

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

// NewSession 开启一个新的空会话，沿用当前会话的厂商、模型和工作区。
//
// 沿用工作区：在一个代码仓库里工作时，新开的会话通常还是同一个仓库；要换仓库，在新会话里重新选择即可。
func (e *Engine) NewSession() (Status, error) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return Status{}, ErrBusy
	}
	e.session = e.freshSessionLocked()
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
	if snap.Provider == "" {
		e.mu.Unlock()
		return TurnResult{}, ErrNoProvider
	}
	p, _, err := e.cfg.Catalog.Get(snap.Provider)
	if err != nil {
		e.mu.Unlock()
		return TurnResult{}, err
	}
	// 运行参数在持锁时取一份副本：UpdateRuntime 可能在本轮结束后修改它们。
	rt := e.runtimeLocked()
	// 空表示没有配置工作区（只在测试里出现：没有工具时不需要工作区），不当作"目录不存在"。
	if rt.Workspace != "" && !isDir(rt.Workspace) {
		e.mu.Unlock()
		return TurnResult{}, fmt.Errorf("工作区 %s 不存在（可能被移动或删除了），请在状态栏上重新选择工作区", rt.Workspace)
	}
	disabled := e.disabledLocked()
	sid := e.session.ID
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
		System:    rt.SystemPrompt,
		MaxTokens: rt.MaxTokens,
		MaxSteps:  rt.MaxSteps,
		Tools:     e.cfg.Tools,
		Policy:    e.cfg.Policy,
		Broker:    e.broker,
		Publisher: e.bus,
		Compactor: e.cfg.Compactor,
		Workspace: rt.Workspace,
		SessionID: sid,
		Disabled:  disabled,
	})
	res := agent.Run(ctx, sess, text)

	// 步骤 3：持久化。即使本轮出错/取消也要保存——用户已经看到的内容不应该丢。
	// 用 context.Background()：本轮被取消时保存仍需完成。
	e.save(sess)

	// 第一轮正常结束后，按设置让模型生成标题。放到后台做：它要再调一次模型（几秒），
	// 不应该让界面一直显示"运行中"、挡住用户的下一条消息。失败就保留截取的第一句，不打扰用户。
	if rt.TitleMode == "model" && res.Reason == event.TurnCompleted && isFirstTurn(sess) {
		before := sess.Snapshot().Title
		go func() {
			tctx := provider.WithCall(context.Background(), provider.CallInfo{SessionID: sess.ID, Source: provider.SourceTitle})
			title, err := generateTitle(tctx, p, snap.Model, sess)
			// 生成期间用户手动改了名，以用户的为准。
			if err != nil || title == "" || sess.Snapshot().Title != before {
				return
			}
			sess.setTitle(title)
			e.save(sess)
			e.bus.Publish(event.New(sess.ID, event.RootAgentID, "", event.SessionUpdated{Title: title}))
		}()
	}
	return res, nil
}

// save 保存会话；失败时发一个非致命的错误事件。
func (e *Engine) save(sess *Session) {
	if e.cfg.Store == nil {
		return
	}
	if err := e.cfg.Store.SaveSession(context.Background(), sess.Snapshot()); err != nil {
		e.bus.Publish(event.New(sess.ID, event.RootAgentID, "", event.Error{
			Code: "store_error", Message: "保存会话失败: " + err.Error(),
		}))
	}
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
// 一次性重放全部历史，给 CLI 用；桌面界面用 LoadSessionTail（只取最近几轮）。
func (e *Engine) LoadSession(ctx context.Context, id string) (Status, error) {
	s, err := e.openSession(ctx, id)
	if err != nil {
		return Status{}, err
	}
	for _, ev := range ReplayEvents(s, e.riskOf) {
		e.bus.Publish(ev)
	}
	return e.Status(), nil
}

// openSession 把 id 对应的历史会话设为当前会话（不重放）。运行中返回 ErrBusy。
func (e *Engine) openSession(ctx context.Context, id string) (*Session, error) {
	if e.cfg.Store == nil {
		return nil, fmt.Errorf("engine: no session store configured")
	}
	// 先快速检查一次，运行中就不必去读数据库；下面替换会话前还会在锁内再检查一次。
	if e.Status().Running {
		return nil, ErrBusy
	}
	s, err := e.cfg.Store.LoadSession(ctx, id)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil, ErrBusy
	}
	// 会话里记录的厂商配置档可能已经被删掉了；此时回落到默认配置档，历史仍然可读。
	if _, _, err := e.cfg.Catalog.Get(s.Provider); err != nil {
		p, m, err2 := e.resolveModel("", "")
		if errors.Is(err2, ErrNoProvider) {
			p, m, err2 = "", "", nil // 一个厂商都没有了：回到"未配置"状态，发送时给出明确的提示
		}
		if err2 == nil {
			s.Provider, s.Model = p, m
		}
	}
	e.session = s
	e.hist = histWindow{}
	e.mu.Unlock()
	e.cfg.Policy.Reset()
	return s, nil
}

func (e *Engine) riskOf(name string) permission.Risk {
	if t, ok := e.cfg.Tools.Get(name); ok {
		return t.Spec().Risk
	}
	return ""
}

// DeleteSession 删除一个已保存的会话。
//
// 删除的是当前会话时，切换到一个新的空会话：否则内存里的会话还在，下一轮结束时
// SaveSession 会把它重新写回数据库，"删掉的会话又回来了"。运行中不允许删除当前会话。
func (e *Engine) DeleteSession(ctx context.Context, id string) error {
	if e.cfg.Store == nil {
		return nil
	}
	e.mu.Lock()
	isCurrent := e.session.ID == id
	if isCurrent && e.running {
		e.mu.Unlock()
		return ErrBusy
	}
	if isCurrent {
		e.session = e.freshSessionLocked()
	}
	e.mu.Unlock()
	if isCurrent {
		e.cfg.Policy.Reset()
	}
	return e.cfg.Store.DeleteSession(ctx, id)
}

func (e *Engine) disabledLocked() map[string]bool {
	m := make(map[string]bool, len(e.cfg.DisabledTools))
	for _, n := range e.cfg.DisabledTools {
		m[n] = true
	}
	return m
}

// ToolInfo 是工具的公开信息，供设置页的"工具开关"展示。
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
	Disabled    bool   `json:"disabled"`
}

// Tools 列出所有已注册的工具（包括 MCP 工具）及其开关状态。
func (e *Engine) Tools() []ToolInfo {
	e.mu.Lock()
	disabled := e.disabledLocked()
	e.mu.Unlock()
	specs := e.cfg.Tools.Specs()
	out := make([]ToolInfo, 0, len(specs))
	for _, s := range specs {
		out = append(out, ToolInfo{Name: s.Name, Description: s.Description, Risk: string(s.Risk), Disabled: disabled[s.Name]})
	}
	return out
}

// SetDisabledTools 设置被禁用的工具，从下一轮开始生效。名字不存在也允许（例如暂时没连上的 MCP 工具）。
func (e *Engine) SetDisabledTools(names []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg.DisabledTools = append([]string(nil), names...)
}

// SetPermissionRules 设置细粒度权限规则，立即对之后的工具调用生效。
func (e *Engine) SetPermissionRules(rules []permission.Rule) error {
	rs, ok := e.cfg.Policy.(permission.RuleSetter)
	if !ok {
		return fmt.Errorf("engine: current permission policy does not support rules")
	}
	return rs.SetRules(rules)
}

// PermissionRules 返回当前的规则。
func (e *Engine) PermissionRules() []permission.Rule {
	if rs, ok := e.cfg.Policy.(permission.RuleSetter); ok {
		return rs.Rules()
	}
	return nil
}

// compacter 是可选能力：存储实现了它，清空历史后会调用来回收磁盘空间（SQLite 的 VACUUM）。
type compacter interface {
	Compact(ctx context.Context) error
}

// DeleteAllSessions 删除所有已保存的会话（设置 → 数据 → 清空历史），并开启一个新的空会话。
// 返回删除的数量。运行中返回 ErrBusy。
func (e *Engine) DeleteAllSessions(ctx context.Context) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return 0, ErrBusy
	}
	n := 0
	if e.cfg.Store != nil {
		list, err := e.cfg.Store.ListSessions(ctx)
		if err != nil {
			return 0, err
		}
		for _, s := range list {
			if err := e.cfg.Store.DeleteSession(ctx, s.ID); err != nil {
				return n, err
			}
			n++
		}
		if c, ok := e.cfg.Store.(compacter); ok {
			if err := c.Compact(ctx); err != nil {
				return n, fmt.Errorf("engine: sessions deleted but compacting the database failed: %w", err)
			}
		}
	}
	// 当前会话也在被清空之列：换成新的空会话，否则下一轮结束时它又会被保存回去。
	e.session = e.freshSessionLocked()
	e.cfg.Policy.Reset()
	return n, nil
}

// freshSessionLocked 创建一个新的空会话，沿用当前会话的厂商、模型和工作区。调用方需持有 e.mu。
func (e *Engine) freshSessionLocked() *Session {
	old := e.session.Snapshot()
	s := NewSession(event.NewID("sess"), old.Provider, old.Model)
	s.Workspace = old.Workspace
	return s
}

// SetSessionWorkspace 为当前会话选择工作区；dir 为空表示改回跟随默认工作区。运行中返回 ErrBusy。
//
// 调用方负责校验目录（必须是已存在的目录、绝对路径）。会话已经有内容时立即保存，
// 这样在会话列表里也能看到新的工作区；空会话等第一轮结束时再保存。
func (e *Engine) SetSessionWorkspace(ctx context.Context, dir string) (Status, error) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return Status{}, ErrBusy
	}
	if e.cfg.WorkspaceLocked {
		e.mu.Unlock()
		return Status{}, fmt.Errorf("工作区由启动参数 -workspace 指定，不能为会话单独选择")
	}
	sess := e.session
	before, _ := e.workspaceLocked(sess)
	sess.setWorkspace(dir)
	after, _ := e.workspaceLocked(sess)
	e.mu.Unlock()

	// 换了目录，"本会话总是允许"的授权是针对旧目录给的，清掉。
	if after != before {
		e.cfg.Policy.Reset()
	}
	if e.cfg.Store != nil && len(sess.messages()) > 0 {
		if err := e.cfg.Store.SaveSession(ctx, sess.Snapshot()); err != nil {
			return e.Status(), err
		}
	}
	return e.Status(), nil
}

// RenameSession 修改会话标题（当前会话或历史会话）。标题去掉首尾空白，不能为空，最长 100 个字符。
//
// 当前会话还没有任何消息时只改内存（空会话不入库，第一轮结束时连同标题一起保存）。
// 改完发 session_updated，界面上的标题和会话列表随之刷新。
func (e *Engine) RenameSession(ctx context.Context, id, title string) error {
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	if title == "" {
		return fmt.Errorf("标题不能为空")
	}
	if r := []rune(title); len(r) > 100 {
		title = string(r[:100])
	}
	e.mu.Lock()
	sess := e.session
	e.mu.Unlock()
	if sess.ID != id {
		if e.cfg.Store == nil {
			return fmt.Errorf("会话 %s 不存在", id)
		}
		s, err := e.cfg.Store.LoadSession(ctx, id)
		if err != nil {
			return err
		}
		sess = s
	}
	sess.setTitle(title)
	if e.cfg.Store != nil && len(sess.messages()) > 0 {
		if err := e.cfg.Store.SaveSession(ctx, sess.Snapshot()); err != nil {
			return err
		}
	}
	e.bus.Publish(event.New(id, event.RootAgentID, "", event.SessionUpdated{Title: title}))
	return nil
}

// GroupStatuses 返回当前厂商目录里各模型组成员的运行状态。
func (e *Engine) GroupStatuses() []provider.GroupStatus {
	e.mu.Lock()
	c := e.cfg.Catalog
	e.mu.Unlock()
	return c.GroupStatuses()
}

// Subagents 返回子 agent 定义注册表（预留扩展点）。
func (e *Engine) Subagents() *SubagentRegistry { return e.cfg.Subagents }
