package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/settings"
	"github.com/crazyjums/myllmapp/internal/workspace"
)

// EventName 是前端订阅的 Wails 事件名。所有内核事件都走这一个通道，由前端按 type 分发。
//
// 为什么不为每种事件类型各开一个 Wails 事件：前端只需注册一次监听，新增事件类型也不需要改绑定层。
// 顺序不靠传输层保证：Wails 开发服务器给每条消息开一个 goroutine 发送，到达浏览器时可能乱序，
// 所以前端按事件信封里单调递增的 seq 重新排序（frontend/src/bridge/reorder.ts）。
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

// ---- 设置：常规 / 系统提示词 / 数据（逻辑在 internal/settings） ----

func (a *App) GetGeneral() settings.GeneralView { return a.settings.General() }

// SaveGeneral 保存常规设置，从下一轮开始生效。运行中会返回错误。
func (a *App) SaveGeneral(g settings.General) (settings.GeneralView, error) {
	if err := a.settings.SaveGeneral(g); err != nil {
		return settings.GeneralView{}, err
	}
	return a.settings.General(), nil
}

// ChooseDirectory 弹出系统的"选择文件夹"对话框；用户取消时返回空字符串。
func (a *App) ChooseDirectory(start string) (string, error) {
	return runtime.OpenDirectoryDialog(a.context(), runtime.OpenDialogOptions{
		Title:                "选择工作区",
		DefaultDirectory:     start,
		CanCreateDirectories: true,
	})
}

func (a *App) GetDataInfo() settings.DataInfo { return a.settings.DataInfo(a.context()) }

// OpenDataDir 用系统文件管理器打开数据目录。
func (a *App) OpenDataDir() error { return openInFileManager(a.settings.DataInfo(a.context()).DataDir) }

// ClearSessions 删除所有历史会话（包括当前会话），返回删除的数量。
func (a *App) ClearSessions() (int, error) { return a.engine.DeleteAllSessions(a.context()) }

// ---- 设置：ChatGPT 账号登录（逻辑在 internal/settings/chatgpt.go 和 internal/siwc） ----

// GetChatGPT 返回 ChatGPT 账号的登录状态。登录进行中时前端轮询它。不含任何令牌。
func (a *App) GetChatGPT() settings.ChatGPTStatus { return a.settings.ChatGPT() }

// LoginChatGPT 开始用 ChatGPT 账号登录：在系统浏览器中打开授权页面，并返回授权地址
// （浏览器没有自动打开时，用户可以复制它）。登录在后台完成。
func (a *App) LoginChatGPT(newAccount bool) (string, error) {
	u, err := a.settings.LoginChatGPT(newAccount)
	if err != nil {
		return "", err
	}
	if a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, u)
	}
	return u, nil
}

func (a *App) CancelChatGPTLogin() { a.settings.CancelChatGPTLogin() }

// LogoutChatGPT 退出登录：吊销令牌并删除本地保存的令牌。
func (a *App) LogoutChatGPT() error { return a.settings.LogoutChatGPT(a.context()) }

// ---- 设置：权限与工具 ----

// ListTools 列出所有工具（含已连接的 MCP 工具）及开关状态。
func (a *App) ListTools() []core.ToolInfo { return a.settings.Tools() }

// SetDisabledTools 设置被禁用的工具，从下一次模型调用开始生效。
func (a *App) SetDisabledTools(names []string) error { return a.settings.SetDisabledTools(names) }

func (a *App) GetPermissionRules() []permission.Rule { return a.settings.PermissionRules() }

// SetPermissionRules 保存细粒度权限规则（整体替换），立即生效。
func (a *App) SetPermissionRules(rules []permission.Rule) error {
	return a.settings.SetPermissionRules(rules)
}

// ---- 设置：状态栏脚本 ----

func (a *App) GetStatusLineScript() string { return a.settings.StatusLineScript() }

func (a *App) SetStatusLineScript(cmd string) error { return a.settings.SetStatusLineScript(cmd) }

// RunStatusLine 运行已保存的状态栏命令。extra 是前端算出的附加信息（上下文占用、花费等），
// 会和会话信息一起以 JSON 写到命令的标准输入。命令本身只从设置文件读取。
func (a *App) RunStatusLine(extra map[string]any) (string, error) {
	return a.settings.RunStatusLine(a.context(), extra)
}

// TestStatusLineScript 试运行设置页里正在编辑的命令，不保存。
func (a *App) TestStatusLineScript(cmd string) (string, error) {
	return a.settings.TestStatusLineScript(a.context(), cmd)
}

// ---- 设置：MCP ----

func (a *App) ListMCPServers() []mcp.ServerConfig { return a.settings.MCPServers() }

// SaveMCPServers 保存整个 MCP server 列表，连接在后台同步。
func (a *App) SaveMCPServers(list []mcp.ServerConfig) error { return a.settings.SaveMCPServers(list) }

// GetMCPStatus 返回各 server 的连接状态，设置页打开时轮询。
func (a *App) GetMCPStatus() []mcp.ServerStatus { return a.settings.MCPStatuses() }

func (a *App) ReconnectMCP(name string) error { return a.settings.ReconnectMCP(name) }

// ---- 设置：外观与钥匙串 ----

// GetUIPrefs 返回界面偏好（主题、字号、语言等）；从未保存过时为 null。
func (a *App) GetUIPrefs() json.RawMessage { return a.settings.UIPrefs() }

func (a *App) SaveUIPrefs(prefs json.RawMessage) error { return a.settings.SaveUIPrefs(prefs) }

func (a *App) GetKeychain() settings.KeychainStatus { return a.settings.Keychain() }

// SetUseKeychain 开启或关闭"API Key 保存到系统钥匙串"，并立即迁移已有的密钥。
func (a *App) SetUseKeychain(on bool) (settings.KeychainStatus, error) {
	if err := a.settings.SetUseKeychain(on); err != nil {
		return settings.KeychainStatus{}, err
	}
	return a.settings.Keychain(), nil
}

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
