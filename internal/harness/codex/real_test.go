package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/harness"
	"github.com/crazyjums/myllmapp/internal/permission"
)

// 对真实 Codex CLI 的冒烟测试：设环境变量 MOSAIC_CODEX_BIN=codex 可执行文件路径才运行（CI 里没有，默认跳过）。
// 用一个空的临时 CODEX_HOME，所以没有登录：验证握手、开线程、发 turn 都按真实协议走通，
// 并且在没有登录这种常见错误下是“给出可读的错误”而不是卡住。
func TestRealCodexUnauthenticated(t *testing.T) {
	bin := os.Getenv("MOSAIC_CODEX_BIN")
	if bin == "" {
		t.Skip("设置 MOSAIC_CODEX_BIN 才会对真实的 codex 运行")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))

	r := &rec{}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	res := New().Run(ctx, harness.Request{
		Text: "说一个字", Workspace: t.TempDir(), Model: "default", PermissionMode: permission.ModeDefault,
		Options: map[string]any{"command": bin}, Emit: r.emit,
		Ask: func(context.Context, permission.Request) (permission.Decision, error) {
			return permission.DecisionDeny, nil
		},
	})
	t.Logf("结果: reason=%s err=%v resume=%q events=%v", res.Reason, res.Err, res.Resume, r.types())
	r.mu.Lock()
	for _, e := range r.events {
		if x, ok := e.p.(event.Error); ok {
			t.Logf("  error[%s]: %.200s", x.Code, x.Message)
		}
	}
	r.mu.Unlock()
	if res.Reason == event.TurnCompleted {
		t.Fatal("没有登录不可能成功")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "codex login") {
		t.Fatalf("没登录应立刻提示 codex login: %v", res.Err)
	}
	if res.Reason != event.TurnError {
		t.Fatalf("应是出错而不是 %s", res.Reason)
	}
}
