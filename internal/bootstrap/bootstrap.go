package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	_ "github.com/crazyjums/myllmapp/internal/provider/all" // 注册所有内置厂商适配器
	"github.com/crazyjums/myllmapp/internal/secret"
	"github.com/crazyjums/myllmapp/internal/settings"
	"github.com/crazyjums/myllmapp/internal/siwc"
	"github.com/crazyjums/myllmapp/internal/store"
	"github.com/crazyjums/myllmapp/internal/tool"
	"github.com/crazyjums/myllmapp/internal/tool/builtin"
)

// Options 是入口层可以覆盖的少量选项（通常来自命令行参数）。
type Options struct {
	// NoStore 为 true 时使用内存存储，不写数据库（调试、一次性会话）。
	NoStore bool
	// PermissionMode 非空时覆盖配置文件中的权限模式。
	PermissionMode string
	// Workspace 非空时强制使用这个工作区（命令行 -workspace），界面上也不能修改。
	Workspace string
	// FallbackWorkspace 是配置文件和界面都没有指定工作区时使用的目录（桌面端的默认工作区）。
	FallbackWorkspace string
	// Secrets 是保存 API Key 的钥匙串；为 nil 时使用系统钥匙串（只有在设置里开启后才会真正读写）。
	Secrets secret.Store
}

// Built 是组装结果。调用方用完后必须调用 Close。
type Built struct {
	Engine *core.Engine
	// Settings 管理界面上配置的厂商；桌面端的设置页和 CLI 的 /model 都通过它修改并持久化。
	Settings  *settings.Manager
	Config    *config.Config
	Workspace string
	DataDir   string
	Close     func() error
}

// Build 按配置组装 Engine。
func Build(cfg *config.Config, opts Options) (*Built, error) {
	// 步骤 1：数据目录与界面设置（settings.json）。界面设置会影响后面的工作区、步数等参数，所以先读。
	dataDir, err := cfg.ResolveDataDir()
	if err != nil {
		return nil, err
	}
	secrets := opts.Secrets
	if secrets == nil {
		secrets = secret.NewKeyring()
	}
	settingsMgr, err := settings.NewManagerWithSecrets(filepath.Join(dataDir, settings.FileName), cfg.Profiles(), provider.DefaultRegistry, secrets)
	if err != nil {
		return nil, err
	}
	// ChatGPT 账号登录：必须在创建厂商目录之前关联，已登录时启动就能选中 chatgpt。
	chatgpt := siwc.NewManager(filepath.Join(dataDir, "chatgpt"), siwc.Options{})
	settingsMgr.AttachChatGPT(chatgpt)

	// 步骤 2：运行参数。优先级：命令行 > 界面设置 > 配置文件 > 内置默认（规则在 settings.ResolveGeneral）。
	// 工作区必须已存在——自动创建可能把文件写到用户意想不到的地方。
	baseWS := cfg.Workspace
	if opts.FallbackWorkspace != "" && (baseWS == "" || baseWS == ".") {
		baseWS = opts.FallbackWorkspace
	}
	if baseWS, err = filepath.Abs(baseWS); err != nil {
		return nil, err
	}
	override := ""
	if opts.Workspace != "" {
		if override, err = filepath.Abs(opts.Workspace); err != nil {
			return nil, err
		}
	}
	defaults := settings.General{
		Workspace:           baseWS,
		MaxSteps:            cfg.MaxSteps,
		MaxTokens:           cfg.MaxTokens,
		ShellTimeoutSeconds: cfg.ShellTimeoutSeconds,
		SystemPrompt:        cfg.SystemPrompt,
	}
	eff, _ := settings.ResolveGeneral(settingsMgr.SavedGeneral(), defaults, override)
	workspace := eff.Workspace
	if fi, err := os.Stat(workspace); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("bootstrap: workspace %q is not an existing directory", workspace)
	}

	// 步骤 3：工具与厂商。单个厂商配置档失败（如缺密钥）不会让启动失败，界面上会显示原因。
	tools := tool.NewRegistry()
	builtin.Register(tools, builtin.Options{ShellTimeout: time.Duration(eff.ShellTimeoutSeconds) * time.Second})
	shell, _ := tools.Get("shell")
	catalog := settingsMgr.Catalog()
	profile, model := pickDefault(catalog, cfg, settingsMgr)

	// 步骤 4：存储。
	var st core.SessionStore
	closeFn := func() error { return nil }
	if opts.NoStore {
		st = store.NewMemory()
	} else {
		sqlite, err := store.OpenSQLite(filepath.Join(dataDir, store.FileName))
		if err != nil {
			return nil, err
		}
		st, closeFn = sqlite, sqlite.Close
	}

	// 步骤 5：权限策略。优先级：命令行参数 > 上次在状态栏上的切换 > 配置文件。
	// 记住的档位不会是 auto（见 settings.SetPermissionMode）；旧版本写进去的 auto 也忽略，
	// 全部放行只能在本次运行中手动开启，或在配置文件/命令行里明确指定。
	mode := cfg.PermissionMode
	if m := settingsMgr.PermissionMode(); m != "" && m != string(permission.ModeAuto) {
		mode = m
	}
	if opts.PermissionMode != "" {
		mode = opts.PermissionMode
	}
	// 命令行传错（例如 -mode autto）要明确报错，而不是悄悄回落到 default。
	if _, err := permission.ParseMode(mode); err != nil {
		closeFn()
		return nil, err
	}

	// 权限规则包在档位外面：命中规则的调用直接放行/拒绝/询问，没命中的再按档位处理。
	policy := permission.NewRulePolicy(permission.NewModePolicy(permission.Mode(mode)), settingsMgr.PermissionRules())

	engine, err := core.NewEngine(core.EngineConfig{
		Catalog:         catalog,
		Tools:           tools,
		Store:           st,
		Policy:          policy,
		Workspace:       workspace,
		WorkspaceLocked: override != "",
		SystemPrompt:    eff.SystemPrompt, // 为空时 Engine 每轮用 core.DefaultSystemPrompt(当前工作区)
		MaxSteps:        eff.MaxSteps,
		MaxTokens:       eff.MaxTokens,
		DefaultProfile:  profile,
		DefaultModel:    model,
		DisabledTools:   settingsMgr.DisabledTools(),
		TitleMode:       eff.TitleMode,
	})
	if err != nil {
		closeFn()
		return nil, err
	}

	// 步骤 6：MCP。在后台连接，连上后工具自动出现在工具表里，不拖慢启动。
	mcpMgr := mcp.NewManager(tools)
	mcpMgr.Apply(settingsMgr.MCPServers())

	settingsMgr.Attach(engine)
	shellTool, _ := shell.(*builtin.Shell)
	settingsMgr.SetRuntimeBase(settings.RuntimeBase{Defaults: defaults, WorkspaceOverride: override, Shell: shellTool, MCP: mcpMgr})
	closeAll := func() error {
		mcpMgr.Close()
		return closeFn()
	}
	return &Built{Engine: engine, Settings: settingsMgr, Config: cfg, Workspace: workspace, DataDir: dataDir, Close: closeAll}, nil
}

// DesktopWorkspace 返回桌面端应使用的工作区；返回空字符串表示沿用配置文件中的设置。
//
// 配置里的默认值 "." 表示"启动时的当前目录"，这对 CLI 很自然，对桌面应用却没有意义：
// 从 Finder / 开始菜单启动时当前目录通常是 / 或安装目录，模型的只读工具（默认无需确认）
// 就能读到整个磁盘。所以桌面端在没有明确配置工作区时，使用数据目录下专门的 workspace 目录（不存在则创建）。
func DesktopWorkspace(cfg *config.Config) (string, error) {
	if cfg.Workspace != "" && cfg.Workspace != "." {
		return "", nil
	}
	dataDir, err := cfg.ResolveDataDir()
	if err != nil {
		return "", err
	}
	ws := filepath.Join(dataDir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return "", fmt.Errorf("bootstrap: create workspace: %w", err)
	}
	return ws, nil
}

// pickDefault 决定启动时使用的厂商和模型：
// 上次在界面上的选择 > 配置文件的 default_provider > 第一个可用的配置档。
// 选中的配置档不可用（例如密钥被删了）时继续往后找，而不是让程序起不来。
func pickDefault(c *provider.Catalog, cfg *config.Config, s *settings.Manager) (string, string) {
	available := func(name string) bool {
		_, _, err := c.Get(name)
		return name != "" && err == nil
	}
	if p, m := s.DefaultSelection(); available(p) {
		return p, m
	}
	if available(cfg.DefaultProvider) {
		return cfg.DefaultProvider, cfg.DefaultModel
	}
	return "", ""
}
