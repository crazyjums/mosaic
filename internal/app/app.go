package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/settings"
	"github.com/crazyjums/myllmapp/internal/workspace"
)

// EventName 是前端订阅的 Wails 事件名。所有内核事件都走这一个通道，由前端按 type 分发。
//
// 为什么不为每种事件类型各开一个 Wails 事件：单通道保证了顺序（不同事件名之间 Wails 不保证顺序），
// 前端只需注册一次监听，新增事件类型也不需要改绑定层。
const EventName = "agent:event"

// EmitFunc 把一个事件发给前端。生产环境是 runtime.EventsEmit，测试时替换成记录函数。
type EmitFunc func(ctx context.Context, name string, data ...any)

// App 是绑定到前端的对象。
type App struct {
	engine   *core.Engine
	settings *settings.Manager
	emit     EmitFunc

	ctx         context.Context
	unsubscribe func()
}

// New 创建绑定对象。emit 为 nil 时使用 Wails 的 runtime.EventsEmit。
func New(engine *core.Engine, s *settings.Manager, emit EmitFunc) *App {
	if emit == nil {
		emit = runtime.EventsEmit
	}
	return &App{engine: engine, settings: s, emit: emit}
}

// Startup 由 Wails 在窗口创建后调用（options.App.OnStartup）。
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.unsubscribe = a.engine.Bus().Subscribe(func(ev event.Event) {
		a.emit(a.ctx, EventName, ev)
	})
}

// Shutdown 由 Wails 在退出前调用：取消正在运行的一轮，停止转发事件。
func (a *App) Shutdown(context.Context) {
	a.engine.Cancel()
	if a.unsubscribe != nil {
		a.unsubscribe()
	}
}

// ---- 以下方法会被绑定到前端 ----

// ProtocolVersion 让前端在启动时检查事件协议版本是否匹配。
func (a *App) ProtocolVersion() int { return event.ProtocolVersion }

func (a *App) GetStatus() core.Status { return a.engine.Status() }

func (a *App) GetProfiles() []provider.ProfileInfo { return a.engine.Profiles() }

func (a *App) ListSessions() ([]core.SessionSummary, error) {
	return a.engine.ListSessions(a.context())
}

func (a *App) NewSession() (core.Status, error) { return a.engine.NewSession() }

// LoadSession 加载历史会话；历史内容会以重放事件的形式通过 agent:event 推给前端。
func (a *App) LoadSession(id string) (core.Status, error) {
	return a.engine.LoadSession(a.context(), id)
}

func (a *App) DeleteSession(id string) error { return a.engine.DeleteSession(a.context(), id) }

// SetModel 切换厂商和模型，并记住选择（下次启动直接使用）。
func (a *App) SetModel(profile, model string) (core.Status, error) {
	if err := a.settings.Select(profile, model); err != nil {
		return core.Status{}, err
	}
	return a.engine.Status(), nil
}

// ---- 设置：厂商配置（逻辑都在 internal/settings） ----

// ListProviders 返回所有厂商配置档，供设置页展示。不含密钥明文。
func (a *App) ListProviders() []settings.ProviderView { return a.settings.Providers() }

// SaveProvider 新建或修改厂商配置档，立即生效。
func (a *App) SaveProvider(in settings.ProviderInput) error { return a.settings.Save(in) }

// DeleteProvider 删除界面上添加的厂商配置档。
func (a *App) DeleteProvider(name string) error { return a.settings.Delete(name) }

// TestProvider 用表单中的配置发一条极短的消息，验证能否连通。不保存。
func (a *App) TestProvider(in settings.ProviderInput) settings.TestResult {
	return a.settings.Test(a.context(), in)
}

// FetchModels 用表单中的 Base URL 和密钥从服务器获取模型列表。
func (a *App) FetchModels(in settings.ProviderInput) ([]string, error) {
	return a.settings.ListModels(a.context(), in)
}

// ---- 状态栏 ----

// GetWorkspaceInfo 返回工作区的 git 分支等信息，状态栏定时调用。
func (a *App) GetWorkspaceInfo() workspace.Info {
	return workspace.Inspect(a.context(), a.engine.Status().Workspace)
}

// SetPermissionMode 切换权限档位（readonly / default / accept_edits / auto），立即生效并记住（auto 除外）。
// "全部放行"的风险确认在前端完成。
func (a *App) SetPermissionMode(mode string) (core.Status, error) {
	if err := a.settings.SetPermissionMode(mode); err != nil {
		return core.Status{}, err
	}
	return a.engine.Status(), nil
}

// GetStatusLineConfig 返回保存的状态栏配置；从未保存过时为 null。
func (a *App) GetStatusLineConfig() json.RawMessage { return a.settings.StatusLine() }

// SaveStatusLineConfig 保存状态栏配置（字段含义由前端定义）。
func (a *App) SaveStatusLineConfig(cfg json.RawMessage) error { return a.settings.SaveStatusLine(cfg) }

// Send 发送一条消息并立即返回；对话过程全部通过事件推送。
//
// 为什么不阻塞到本轮结束：Wails 的方法调用对前端来说是一个 Promise，一轮对话可能持续几分钟，
// 期间还需要调用 RespondPermission；异步返回让前端的调用模型保持简单（"发出去，然后听事件"）。
func (a *App) Send(text string) error {
	if a.engine.Status().Running {
		return core.ErrBusy
	}
	go func() {
		// 极少数情况下两次 Send 几乎同时到达，第二次会在这里拿到 ErrBusy，通过错误事件告知前端。
		if _, err := a.engine.Send(a.context(), text); err != nil {
			code := "send_failed"
			if errors.Is(err, core.ErrBusy) {
				code = "busy"
			}
			// 发布到总线即可，Startup 中的订阅会把它转发给前端。
			st := a.engine.Status()
			a.engine.Bus().Publish(event.New(st.SessionID, event.RootAgentID, "",
				event.Error{Code: code, Message: err.Error(), Fatal: true}))
		}
	}()
	return nil
}

func (a *App) Cancel() { a.engine.Cancel() }

func (a *App) RespondPermission(requestID, decision string) error {
	return a.engine.RespondPermission(requestID, decision)
}

func (a *App) context() context.Context {
	if a.ctx == nil {
		return context.Background()
	}
	return a.ctx
}
