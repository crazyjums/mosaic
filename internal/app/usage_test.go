package app

import (
	"context"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/bootstrap"
	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/settings"
)

// 一次真实的对话（fake 演示厂商）会进账本，报表能读到；模型组和代理的绑定可用。
func TestUsageGroupsAndProxyBindings(t *testing.T) {
	cfg := config.Default()
	cfg.EnableDemo()
	cfg.DataDir = t.TempDir()
	cfg.Providers[0].Options = map[string]any{}
	built, err := bootstrap.Build(cfg, bootstrap.Options{Workspace: t.TempDir(), NoStore: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	a := New(built.Engine, built.Settings, func(_ context.Context, _ string, data ...any) {
		if ev, ok := data[0].(event.Event); ok && ev.Type == event.TypeTurnEnd {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	})
	a.Startup(context.Background())

	if g := a.GetGroups(); g == nil || len(g) != 0 {
		t.Fatalf("没有模型组时应返回空数组而不是 null: %#v", g)
	}
	if st := a.GetGroupStatuses(); st == nil || len(st) != 0 {
		t.Fatalf("状态也一样: %#v", st)
	}
	if err := a.SetProxy("socks5://u:pw@127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	if p := a.GetProxy(); !p.Set || p.Display == "socks5://u:pw@127.0.0.1:1080" {
		t.Fatalf("代理显示要脱敏: %+v", p)
	}

	if _, err := a.SetPermissionMode("auto"); err != nil {
		t.Fatal(err)
	}
	if err := a.Send("演示一下"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("对话没有结束")
	}
	built.Close() // 等异步写入的账本写完
	rep, err := a.GetUsageReport(settings.UsageRequest{Range: "today", By: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Totals.Requests == 0 || len(rep.Nodes) != 1 {
		t.Fatalf("fake 厂商的调用应该进账本: %+v", rep.Totals)
	}
}
