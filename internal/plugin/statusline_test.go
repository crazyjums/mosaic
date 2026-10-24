package plugin

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCommandStatusLine(t *testing.T) {
	// 读取标准输入里的 JSON，输出第一行；Windows 的 cmd 没有方便的 JSON 工具，只验证基本输出。
	cmd := `read line; echo "$line" | grep -o '"model":"[^"]*"'; echo second-line`
	if runtime.GOOS == "windows" {
		cmd = "echo hello& echo second-line"
	}
	out, err := CommandStatusLine{Command: cmd, Dir: t.TempDir()}.Render(context.Background(), StatusInfo{Model: "gpt-x", Extra: map[string]any{"cost": 1.5}})
	if err != nil {
		t.Fatal(err)
	}
	want := `"model":"gpt-x"`
	if runtime.GOOS == "windows" {
		want = "hello"
	}
	if strings.TrimSpace(out) != want {
		t.Fatalf("应只取第一行: %q", out)
	}
}

func TestCommandStatusLineErrors(t *testing.T) {
	if _, err := (CommandStatusLine{}).Render(context.Background(), StatusInfo{}); err == nil {
		t.Fatal("空命令应报错")
	}
	_, err := CommandStatusLine{Command: "echo oops 1>&2 && exit 3"}.Render(context.Background(), StatusInfo{})
	if err == nil || !strings.Contains(err.Error(), "oops") {
		t.Fatalf("失败时应带上 stderr: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	start := time.Now()
	_, err = CommandStatusLine{Command: "sleep 10", Timeout: 200 * time.Millisecond}.Render(context.Background(), StatusInfo{})
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 3*time.Second {
		t.Fatalf("应超时: %v %s", err, time.Since(start))
	}
}
