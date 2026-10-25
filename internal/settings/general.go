package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/store"
	"github.com/crazyjums/myllmapp/internal/tool/builtin"
)

// General 是"设置 → 常规 / 系统提示词"中的参数。零值字段表示"没有在界面上设置，沿用配置文件或内置默认值"。
//
// 用零值表示"未设置"而不是指针：JSON 简洁，界面上"清空输入框 = 恢复默认"也很自然。
// 代价是不能把某个值显式设为 0——这几个参数为 0 本来就没有意义（0 步、0 秒超时）。
type General struct {
	Workspace           string `json:"workspace,omitempty"`
	MaxSteps            int    `json:"max_steps,omitempty"`
	MaxTokens           int    `json:"max_tokens,omitempty"`
	ShellTimeoutSeconds int    `json:"shell_timeout_seconds,omitempty"`
	SystemPrompt        string `json:"system_prompt,omitempty"`
	// TitleMode 决定会话标题怎么来：空或 first_message 截取第一句，model 由模型生成。
	TitleMode string `json:"title_mode,omitempty"`
}

// 会话标题的生成方式。
const (
	TitleFirstMessage = "first_message"
	TitleModel        = "model"
)

// 校验上限：防止误输入（多打几个 0）造成意外的费用或长时间阻塞。
const (
	maxMaxSteps         = 500
	maxMaxTokens        = 2_000_000
	maxShellTimeoutSecs = 24 * 3600
	maxSystemPromptLen  = 100_000 // 字符
)

// RuntimeBase 是"界面没有设置时"使用的值和需要的钩子，由 bootstrap 在启动时提供。
type RuntimeBase struct {
	// Defaults 来自 config.toml 或内置默认值（Workspace 已解析为绝对路径）。
	Defaults General
	// WorkspaceOverride 非空表示工作区由命令行 -workspace 指定，界面上不能修改。
	WorkspaceOverride string
	// Shell 是内置 shell 工具，用于修改超时后立即生效。
	Shell *builtin.Shell
	// MCP 管理 MCP server 的连接；为 nil 时（例如测试）只保存配置。
	MCP *mcp.Manager
}

// GeneralView 是发给前端的视图。
type GeneralView struct {
	// Saved 是界面上保存的值（零值表示未设置）。
	Saved General `json:"saved"`
	// Effective 是当前实际生效的值（已补上默认值）。
	Effective General `json:"effective"`
	// Defaults 是界面上不设置时使用的值，前端用作输入框的占位提示。
	Defaults General `json:"defaults"`
	// DefaultSystemPrompt 是内置默认提示词（按当前工作区生成），供"系统提示词"页预览。
	DefaultSystemPrompt string `json:"default_system_prompt"`
	// WorkspaceLocked 为 true 表示工作区由命令行参数指定，界面上不能修改。
	WorkspaceLocked bool `json:"workspace_locked"`
	// WorkspaceWarning 非空表示保存的工作区目前不可用（例如目录被删除），已回落到默认值。
	WorkspaceWarning string `json:"workspace_warning,omitempty"`
}

// DataInfo 是"设置 → 数据"页显示的信息。
type DataInfo struct {
	DataDir      string `json:"data_dir"`
	SettingsPath string `json:"settings_path"`
	DBPath       string `json:"db_path"`
	// DBBytes 是会话数据库占用的磁盘空间（含 WAL 文件）。
	DBBytes  int64 `json:"db_bytes"`
	Sessions int   `json:"sessions"`
}

// SetRuntimeBase 由 bootstrap 在创建 Engine 后调用。
func (m *Manager) SetRuntimeBase(b RuntimeBase) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rtBase = b
}

// SavedGeneral 返回界面上保存的常规设置，bootstrap 启动时用它计算初始值。
func (m *Manager) SavedGeneral() General {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.file.General
}

// ResolveGeneral 按优先级计算实际生效的值：命令行 > 界面设置 > 配置文件 > 内置默认。
// 返回的 warning 非空表示界面上保存的工作区当前不可用。
//
// 导出给 bootstrap 使用，这样启动时和界面修改后用的是同一套规则。
func ResolveGeneral(saved, defaults General, workspaceOverride string) (eff General, warning string) {
	eff = defaults
	if saved.Workspace != "" {
		if err := checkDir(saved.Workspace); err != nil {
			warning = fmt.Sprintf("保存的工作区 %s 不可用（%v），已使用默认工作区", saved.Workspace, err)
		} else {
			eff.Workspace = saved.Workspace
		}
	}
	if workspaceOverride != "" {
		eff.Workspace = workspaceOverride
	}
	if saved.MaxSteps > 0 {
		eff.MaxSteps = saved.MaxSteps
	}
	if saved.MaxTokens > 0 {
		eff.MaxTokens = saved.MaxTokens
	}
	if saved.ShellTimeoutSeconds > 0 {
		eff.ShellTimeoutSeconds = saved.ShellTimeoutSeconds
	}
	if saved.SystemPrompt != "" {
		eff.SystemPrompt = saved.SystemPrompt
	}
	if saved.TitleMode != "" {
		eff.TitleMode = saved.TitleMode
	}
	return eff, warning
}

// displayDefaults 把"0 = 使用内置默认"展开成具体数字，方便界面显示。
func displayDefaults(g General) General {
	if g.MaxSteps == 0 {
		g.MaxSteps = core.DefaultMaxSteps
	}
	if g.TitleMode == "" {
		g.TitleMode = TitleFirstMessage
	}
	if g.ShellTimeoutSeconds == 0 {
		g.ShellTimeoutSeconds = int(builtin.DefaultShellTimeout / time.Second)
	}
	return g
}

// General 返回常规设置的视图。
func (m *Manager) General() GeneralView {
	m.mu.Lock()
	defer m.mu.Unlock()
	eff, warning := ResolveGeneral(m.file.General, m.rtBase.Defaults, m.rtBase.WorkspaceOverride)
	return GeneralView{
		Saved:               m.file.General,
		Effective:           displayDefaults(eff),
		Defaults:            displayDefaults(m.rtBase.Defaults),
		DefaultSystemPrompt: core.DefaultSystemPrompt(eff.Workspace),
		WorkspaceLocked:     m.rtBase.WorkspaceOverride != "",
		WorkspaceWarning:    warning,
	}
}

// SaveGeneral 保存常规设置并立即生效（从下一轮开始）。运行中返回 core.ErrBusy。
func (m *Manager) SaveGeneral(g General) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine == nil {
		return errors.New("settings: engine not attached")
	}

	// 步骤 1：校验。
	if g.Workspace != "" {
		abs, err := filepath.Abs(g.Workspace)
		if err != nil {
			return err
		}
		if err := checkDir(abs); err != nil {
			return fmt.Errorf("工作区不可用：%v", err)
		}
		g.Workspace = abs
	}
	switch {
	case g.MaxSteps < 0 || g.MaxSteps > maxMaxSteps:
		return fmt.Errorf("最大步数应在 1～%d 之间", maxMaxSteps)
	case g.MaxTokens < 0 || g.MaxTokens > maxMaxTokens:
		return fmt.Errorf("输出上限应在 1～%d 之间", maxMaxTokens)
	case g.ShellTimeoutSeconds < 0 || g.ShellTimeoutSeconds > maxShellTimeoutSecs:
		return fmt.Errorf("命令超时应在 1～%d 秒之间", maxShellTimeoutSecs)
	case utf8.RuneCountInString(g.SystemPrompt) > maxSystemPromptLen:
		return fmt.Errorf("系统提示词过长（上限 %d 字）", maxSystemPromptLen)
	case g.TitleMode != "" && g.TitleMode != TitleFirstMessage && g.TitleMode != TitleModel:
		return fmt.Errorf("未知的标题方式 %q", g.TitleMode)
	}

	// 步骤 2：应用到 Engine（运行中会返回 ErrBusy，此时什么都不改）和 shell 工具。
	eff, _ := ResolveGeneral(g, m.rtBase.Defaults, m.rtBase.WorkspaceOverride)
	if err := m.engine.UpdateRuntime(core.RuntimeConfig{
		Workspace:    eff.Workspace,
		SystemPrompt: eff.SystemPrompt,
		MaxSteps:     eff.MaxSteps,
		MaxTokens:    eff.MaxTokens,
		TitleMode:    eff.TitleMode,
	}); err != nil {
		return err
	}
	if m.rtBase.Shell != nil {
		m.rtBase.Shell.SetTimeout(time.Duration(eff.ShellTimeoutSeconds) * time.Second)
	}

	// 步骤 3：持久化。
	next := cloneFile(m.file)
	next.General = g
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

// SetSessionWorkspace 为当前会话选择工作区（dir 为空表示跟随默认工作区）。目录必须已存在。
func (m *Manager) SetSessionWorkspace(ctx context.Context, dir string) (core.Status, error) {
	m.mu.Lock()
	eng := m.engine
	m.mu.Unlock()
	if eng == nil {
		return core.Status{}, errors.New("settings: engine not attached")
	}
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return core.Status{}, err
		}
		if err := checkDir(abs); err != nil {
			return core.Status{}, fmt.Errorf("工作区不可用：%v", err)
		}
		dir = abs
	}
	return eng.SetSessionWorkspace(ctx, dir)
}

// DataInfo 返回数据目录的信息。
func (m *Manager) DataInfo(ctx context.Context) DataInfo {
	dir := filepath.Dir(m.path)
	info := DataInfo{DataDir: dir, SettingsPath: m.path, DBPath: filepath.Join(dir, store.FileName)}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if fi, err := os.Stat(info.DBPath + suffix); err == nil {
			info.DBBytes += fi.Size()
		}
	}
	m.mu.Lock()
	eng := m.engine
	m.mu.Unlock()
	if eng != nil {
		if list, err := eng.ListSessions(ctx); err == nil {
			info.Sessions = len(list)
		}
	}
	return info
}

func checkDir(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s 不是目录", p)
	}
	return nil
}
