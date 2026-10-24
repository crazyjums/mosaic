package bootstrap

import (
	"context"
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
