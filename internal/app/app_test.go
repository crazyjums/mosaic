package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/bootstrap"
	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/event"
)

// 绑定层测试：不需要 Wails 运行时，用记录函数替换 EventsEmit。
func TestAppForwardsEventsAndCommands(t *testing.T) {
	cfg := config.Default()
	cfg.EnableDemo()
	cfg.DataDir = t.TempDir()
	cfg.Providers[0].Options = map[string]any{} // demo 剧本、无延迟
	built, err := bootstrap.Build(cfg, bootstrap.Options{Workspace: t.TempDir(), NoStore: true})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()

	var mu sync.Mutex
	var got []event.Event
	var a *App
	a = New(built.Engine, built.Settings, func(_ context.Context, name string, data ...any) {
		if name != EventName {
			t.Errorf("unexpected event name %s", name)
		}
		ev := data[0].(event.Event)
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		// 模拟前端：收到权限请求就点"允许"。
		if p, ok := ev.Data.(event.PermissionRequest); ok {
			go a.RespondPermission(p.RequestID, "allow")
		}
	})
	a.Startup(context.Background())
	defer a.Shutdown(context.Background())

	if a.ProtocolVersion() != event.ProtocolVersion {
		t.Fatal("protocol version")
	}
	if err := a.Send("hi"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		done := n > 0 && got[n-1].Type == event.TypeTurnEnd
		mu.Unlock()
		if done {
			break
		}
		select {
		case <-deadline:
			t.Fatal("等待 turn_end 超时")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if a.GetStatus().Running {
		t.Fatal("本轮结束后不应处于运行状态")
	}
	if _, err := a.SetModel("fake", "echo"); err != nil {
		t.Fatal(err)
	}
	if st := a.GetStatus(); st.Model != "echo" {
		t.Fatalf("status: %+v", st)
	}
	if p, m := built.Settings.DefaultSelection(); p != "fake" || m != "echo" {
		t.Fatalf("SetModel 应记住选择: %s %s", p, m)
	}
	if len(a.ListProviders()) == 0 {
		t.Fatal("ListProviders 不应为空")
	}
}
