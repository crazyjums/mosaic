package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/settings"
)

// 用内置默认配置组装一次，确保"开箱即用"：fake 可用、缺密钥的厂商显示为不可用而不是导致启动失败。
func TestBuildWithDefaults(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Providers[0].Options = map[string]any{"scenario": "echo"}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")

	b, err := Build(cfg, Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	st := b.Engine.Status()
	if st.Provider != "fake" || st.Model != "demo" {
		t.Fatalf("status: %+v", st)
	}
	for _, p := range b.Engine.Profiles() {
		if p.Name != "fake" && p.Available {
			t.Errorf("%s 没有密钥，应不可用", p.Name)
		}
	}

	var rec event.Recorder
	b.Engine.Bus().Subscribe(rec.Handle)
	res, err := b.Engine.Send(context.Background(), "ping")
	if err != nil || res.Reason != event.TurnCompleted {
		t.Fatalf("send: %v %+v", err, res)
	}
	// 会话写进了 SQLite。
	list, err := b.Engine.ListSessions(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("sessions: %v %+v", err, list)
	}
}

func TestBuildRejectsMissingWorkspace(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	if _, err := Build(cfg, Options{Workspace: "/no/such/dir", NoStore: true}); err == nil {
		t.Fatal("工作区不存在应报错")
	}
}

// 界面上保存过的厂商和选择，重启后应直接生效，而不是回到演示厂商。
func TestBuildUsesRememberedSelection(t *testing.T) {
	dataDir := t.TempDir()
	f := settings.File{
		DefaultProvider: "local",
		DefaultModel:    "m2",
		Providers: []settings.ProviderSetting{
			{Name: "local", Type: "openai", BaseURL: "http://localhost:11434/v1", Models: []string{"m1", "m2"},
				Options: map[string]any{"allow_no_key": true}},
		},
	}
	if err := settings.Save(filepath.Join(dataDir, settings.FileName), f); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = dataDir
	b, err := Build(cfg, Options{Workspace: t.TempDir(), NoStore: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if st := b.Engine.Status(); st.Provider != "local" || st.Model != "m2" {
		t.Fatalf("应使用上次的选择: %+v", st)
	}
}

// 上次选择的厂商变得不可用（例如密钥失效被删）时，应回落而不是启动失败。
func TestBuildFallsBackWhenRememberedUnavailable(t *testing.T) {
	dataDir := t.TempDir()
	_ = settings.Save(filepath.Join(dataDir, settings.FileName), settings.File{DefaultProvider: "anthropic"})
	t.Setenv("ANTHROPIC_API_KEY", "")
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DefaultProvider = "anthropic"
	b, err := Build(cfg, Options{Workspace: t.TempDir(), NoStore: true})
	if err != nil {
		t.Fatalf("不应因为默认厂商不可用而启动失败: %v", err)
	}
	defer b.Close()
	if st := b.Engine.Status(); st.Provider != "fake" {
		t.Fatalf("应回落到第一个可用的厂商: %+v", st)
	}
}

// 桌面端没有明确配置工作区时，不能用启动时的当前目录（可能是 /），而是用数据目录下的专门目录。
func TestDesktopWorkspace(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	ws, err := DesktopWorkspace(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfg.DataDir, "workspace"); ws != want {
		t.Fatalf("workspace = %q, want %q", ws, want)
	}
	if fi, err := os.Stat(ws); err != nil || !fi.IsDir() {
		t.Fatalf("应创建工作区目录: %v", err)
	}

	cfg.Workspace = "/some/project"
	if ws, _ := DesktopWorkspace(cfg); ws != "" {
		t.Fatalf("明确配置了工作区时应沿用配置: %q", ws)
	}
}

// 工作区优先级：命令行 > 界面设置 > 配置文件 > 桌面端默认目录。
func TestBuildWorkspacePriority(t *testing.T) {
	dataDir, fallback, fromSettings, fromFlag := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	build := func(opts Options) string {
		cfg := config.Default()
		cfg.DataDir = dataDir
		opts.NoStore = true
		b, err := Build(cfg, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		return b.Engine.Status().Workspace
	}
	if ws := build(Options{FallbackWorkspace: fallback}); ws != fallback {
		t.Fatalf("配置为默认的 . 时应使用桌面端默认目录: %s", ws)
	}
	_ = settings.Save(filepath.Join(dataDir, settings.FileName), settings.File{General: settings.General{Workspace: fromSettings, MaxSteps: 5}})
	if ws := build(Options{FallbackWorkspace: fallback}); ws != fromSettings {
		t.Fatalf("界面设置应优先于默认目录: %s", ws)
	}
	if ws := build(Options{FallbackWorkspace: fallback, Workspace: fromFlag}); ws != fromFlag {
		t.Fatalf("命令行参数优先级最高: %s", ws)
	}
}
