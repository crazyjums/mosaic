package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	_ "github.com/crazyjums/myllmapp/internal/provider/all" // 注册所有内置厂商适配器
	"github.com/crazyjums/myllmapp/internal/settings"
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
	// Workspace 非空时覆盖配置文件中的工作区。
	Workspace string
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
	// 步骤 1：路径。工作区必须已存在——自动创建可能把文件写到用户意想不到的地方。
	wsPath := cfg.Workspace
	if opts.Workspace != "" {
		wsPath = opts.Workspace
	}
	workspace, err := filepath.Abs(wsPath)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(workspace); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("bootstrap: workspace %q is not an existing directory", workspace)
	}
	dataDir, err := cfg.ResolveDataDir()
	if err != nil {
		return nil, err
	}

	// 步骤 2：工具。
	tools := tool.NewRegistry()
	shellTimeout := time.Duration(cfg.ShellTimeoutSeconds) * time.Second
	builtin.Register(tools, builtin.Options{ShellTimeout: shellTimeout})

	// 步骤 3：厂商。配置文件中的配置档 + 界面上配置的配置档（settings.json）合并。
	// 单个配置档失败（如缺密钥）不会让启动失败，界面上会显示原因。
	settingsMgr, err := settings.NewManager(filepath.Join(dataDir, settings.FileName), cfg.Profiles(), provider.DefaultRegistry)
	if err != nil {
		return nil, err
	}
	catalog := settingsMgr.Catalog()
	profile, model := pickDefault(catalog, cfg, settingsMgr)

	// 步骤 4：存储。
	var st core.SessionStore
	closeFn := func() error { return nil }
	if opts.NoStore {
		st = store.NewMemory()
	} else {
		sqlite, err := store.OpenSQLite(filepath.Join(dataDir, "sessions.db"))
		if err != nil {
			return nil, err
		}
		st, closeFn = sqlite, sqlite.Close
	}

	// 步骤 5：权限策略。优先级：命令行参数 > 上次在状态栏上的切换 > 配置文件。
	mode := cfg.PermissionMode
	if m := settingsMgr.PermissionMode(); m != "" {
		mode = m
	}
	if opts.PermissionMode != "" {
		mode = opts.PermissionMode
	}

	// 步骤 6：系统提示词。
	system := cfg.SystemPrompt
	if system == "" {
		system = DefaultSystemPrompt(workspace)
	}

	engine, err := core.NewEngine(core.EngineConfig{
		Catalog:        catalog,
		Tools:          tools,
		Store:          st,
		Policy:         permission.NewModePolicy(permission.Mode(mode)),
		Workspace:      workspace,
		SystemPrompt:   system,
		MaxSteps:       cfg.MaxSteps,
		MaxTokens:      cfg.MaxTokens,
		DefaultProfile: profile,
		DefaultModel:   model,
	})
	if err != nil {
		closeFn()
		return nil, err
	}
	settingsMgr.Attach(engine)
	return &Built{Engine: engine, Settings: settingsMgr, Config: cfg, Workspace: workspace, DataDir: dataDir, Close: closeFn}, nil
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

// DefaultSystemPrompt 生成默认系统提示词。
//
// 刻意保持简短：只交代环境事实（工作区、系统）和工具使用约定。
// 注意不放当前时间之类每次都变的内容，以免破坏厂商侧的提示缓存。
func DefaultSystemPrompt(workspace string) string {
	return fmt.Sprintf(`You are a helpful coding agent running inside a desktop client.

Environment:
- Workspace root: %s (all file paths are relative to it)
- OS: %s/%s

Guidelines:
- Use the provided tools to inspect and change files instead of guessing their contents.
- Writing files and running shell commands require the user's approval; if a call is denied, adapt instead of retrying the same call.
- Keep answers concise. Reply in the same language the user writes in.`, workspace, runtime.GOOS, runtime.GOARCH)
}
