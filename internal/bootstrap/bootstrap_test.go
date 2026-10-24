package bootstrap

import (
	"context"
	"testing"

	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/event"
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
