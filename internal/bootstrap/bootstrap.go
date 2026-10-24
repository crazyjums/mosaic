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
	Engine    *core.Engine
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

	// 步骤 3：厂商。单个配置档失败（如缺密钥）不会让启动失败，界面上会显示原因。
	catalog := provider.NewCatalog(provider.DefaultRegistry, cfg.Profiles())

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

	// 步骤 5：权限策略。
	mode := cfg.PermissionMode
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
		DefaultProfile: cfg.DefaultProvider,
		DefaultModel:   cfg.DefaultModel,
	})
	if err != nil {
		closeFn()
		return nil, err
	}
	return &Built{Engine: engine, Config: cfg, Workspace: workspace, DataDir: dataDir, Close: closeFn}, nil
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
