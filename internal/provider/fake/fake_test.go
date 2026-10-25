package fake

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
)

func collect(t *testing.T, p provider.Provider, msgs []provider.Message) ([]provider.Delta, error) {
	t.Helper()
	var out []provider.Delta
	err := p.Stream(context.Background(), provider.Request{Messages: msgs}, func(d provider.Delta) { out = append(out, d) })
	return out, err
}

func TestDemoProgressesByStep(t *testing.T) {
	p, err := New(provider.ProfileConfig{Name: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	user := provider.UserText(`say "hi"`)

	ds, _ := collect(t, p, []provider.Message{user})
	var call *provider.ToolCall
	for _, d := range ds {
		if d.Type == provider.DeltaToolCall {
			call = d.ToolCall
		}
	}
	if call == nil || call.Name != "list_dir" {
		t.Fatalf("第 1 步应调用 list_dir: %+v", ds)
	}
	if last := ds[len(ds)-1]; last.Type != provider.DeltaStop || last.StopReason != provider.StopToolUse {
		t.Fatalf("应以 tool_use 结束: %+v", last)
	}

	// 第 2 步：带引号的用户输入被正确转义进 JSON。
	asst := provider.Message{Role: provider.RoleAssistant}
	ds, _ = collect(t, p, []provider.Message{user, asst, {Role: provider.RoleUser}})
	for _, d := range ds {
		if d.Type == provider.DeltaToolCall {
			var in map[string]string
			if err := json.Unmarshal(d.ToolCall.Input, &in); err != nil {
				t.Fatalf("参数应为合法 JSON: %s", d.ToolCall.Input)
			}
		}
	}
}

func TestEchoScenario(t *testing.T) {
	p, _ := New(provider.ProfileConfig{Name: "e", Options: map[string]any{"scenario": "echo"}})
	ds, _ := collect(t, p, []provider.Message{provider.UserText("ping")})
	text := ""
	for _, d := range ds {
		if d.Type == provider.DeltaText {
			text += d.Text
		}
	}
	if text != "（fake）你说的是：ping" {
		t.Fatalf("echo: %q", text)
	}
}

func TestScriptFileAndError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	_ = os.WriteFile(path, []byte(`{"steps":[{"text":"x","error":"boom"}]}`), 0o644)
	p, err := New(provider.ProfileConfig{Options: map[string]any{"script": path}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, p, []provider.Message{provider.UserText("a")}); err == nil {
		t.Fatal("应返回剧本中的错误")
	}
	if _, err := New(provider.ProfileConfig{Options: map[string]any{"scenario": "nope"}}); err == nil {
		t.Fatal("未知场景应报错")
	}
}

func TestCancelDuringDelay(t *testing.T) {
	p := NewWithScript("f", DemoScript(), 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	err := p.Stream(ctx, provider.Request{Messages: []provider.Message{provider.UserText("a")}}, func(provider.Delta) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应尽快返回取消错误: %v", err)
	}
}
