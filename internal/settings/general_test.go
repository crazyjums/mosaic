package settings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/tool/builtin"
)

func newGeneralEnv(t *testing.T, override string) (*Manager, *core.Engine, *builtin.Shell, string) {
	t.Helper()
	m, eng, path := newEnv(t)
	base := t.TempDir()
	shell := builtin.NewShell(0, 0)
	_ = eng.UpdateRuntime(core.RuntimeConfig{Workspace: base})
	m.SetRuntimeBase(RuntimeBase{Defaults: General{Workspace: base}, WorkspaceOverride: override, Shell: shell})
	return m, eng, shell, path
}

func TestResolveGeneralPriority(t *testing.T) {
	dir := t.TempDir()
	defaults := General{Workspace: "/base", MaxSteps: 10, ShellTimeoutSeconds: 30}
	eff, warn := ResolveGeneral(General{Workspace: dir, MaxSteps: 50}, defaults, "")
	if eff.Workspace != dir || eff.MaxSteps != 50 || eff.ShellTimeoutSeconds != 30 || warn != "" {
		t.Fatalf("界面设置应覆盖配置文件，未设置的沿用: %+v %q", eff, warn)
	}
	if eff, _ := ResolveGeneral(General{Workspace: dir}, defaults, "/flag"); eff.Workspace != "/flag" {
		t.Fatal("命令行参数优先级最高")
	}
	eff, warn = ResolveGeneral(General{Workspace: filepath.Join(dir, "deleted")}, defaults, "")
	if eff.Workspace != "/base" || warn == "" {
		t.Fatalf("保存的工作区不存在时应回落并给出提示: %+v %q", eff, warn)
	}
}

func TestSaveGeneralAppliesImmediately(t *testing.T) {
	m, eng, shell, path := newGeneralEnv(t, "")
	ws := t.TempDir()
	g := General{Workspace: ws, MaxSteps: 7, MaxTokens: 4096, ShellTimeoutSeconds: 5, SystemPrompt: "你是一个测试助手"}
	if err := m.SaveGeneral(g); err != nil {
		t.Fatal(err)
	}
	rt := eng.Runtime()
	if rt.Workspace != ws || rt.MaxSteps != 7 || rt.MaxTokens != 4096 || rt.SystemPrompt != "你是一个测试助手" {
		t.Fatalf("应立即应用到 Engine: %+v", rt)
	}
	if eng.Status().Workspace != ws {
		t.Fatal("状态里的工作区应更新")
	}
	if shell.Timeout() != 5*time.Second {
		t.Fatalf("shell 超时应立即生效: %s", shell.Timeout())
	}

	// 清空字段 = 恢复默认；系统提示词恢复为内置默认。
	if err := m.SaveGeneral(General{}); err != nil {
		t.Fatal(err)
	}
	v := m.General()
	if v.Effective.MaxSteps != core.DefaultMaxSteps || v.Effective.ShellTimeoutSeconds != 60 || eng.Runtime().SystemPrompt != "" {
		t.Fatalf("清空后应恢复默认: %+v / %+v", v.Effective, eng.Runtime())
	}
	if !strings.Contains(v.DefaultSystemPrompt, v.Effective.Workspace) {
		t.Fatal("默认提示词应包含当前工作区")
	}

	// 持久化。
	_ = m.SaveGeneral(General{MaxSteps: 9})
	m2, _ := NewManager(path, nil, provider.DefaultRegistry)
	if m2.SavedGeneral().MaxSteps != 9 {
		t.Fatal("应写入 settings.json")
	}
}

func TestSaveGeneralValidation(t *testing.T) {
	m, eng, _, _ := newGeneralEnv(t, "")
	file := filepath.Join(t.TempDir(), "f")
	os.WriteFile(file, nil, 0o644)
	bad := []General{
		{Workspace: filepath.Join(t.TempDir(), "nope")},
		{Workspace: file},
		{MaxSteps: -1},
		{MaxSteps: maxMaxSteps + 1},
		{MaxTokens: maxMaxTokens + 1},
		{ShellTimeoutSeconds: -5},
		{SystemPrompt: strings.Repeat("字", maxSystemPromptLen+1)},
	}
	before := eng.Runtime()
	for _, g := range bad {
		if err := m.SaveGeneral(g); err == nil {
			t.Errorf("应校验失败: %+v", g)
		}
	}
	if eng.Runtime() != before {
		t.Fatal("校验失败时不应修改任何东西")
	}
}

func TestWorkspaceLockedByFlag(t *testing.T) {
	m, eng, _, _ := newGeneralEnv(t, "/from/flag")
	if !m.General().WorkspaceLocked {
		t.Fatal("命令行指定工作区时应标记为锁定")
	}
	_ = m.SaveGeneral(General{Workspace: t.TempDir(), MaxSteps: 3})
	if eng.Runtime().Workspace != "/from/flag" {
		t.Fatal("命令行指定的工作区不应被界面设置覆盖")
	}
}

func TestDataInfo(t *testing.T) {
	m, _, _, path := newGeneralEnv(t, "")
	info := m.DataInfo(context.Background())
	if info.DataDir != filepath.Dir(path) || !strings.HasSuffix(info.DBPath, "sessions.db") {
		t.Fatalf("info: %+v", info)
	}
}
