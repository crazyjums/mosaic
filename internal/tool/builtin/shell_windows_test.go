//go:build windows

package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/crazyjums/myllmapp/internal/tool"
)

func runShell(t *testing.T, command string) tool.Result {
	t.Helper()
	sh := NewShell(10*time.Second, 1<<20)
	in, _ := json.Marshal(map[string]string{"command": command})
	return sh.Execute(context.Background(), tool.Call{Input: in, Workspace: t.TempDir()})
}

// 带引号的命令必须原样交给 cmd：Go 默认会把 " 转义成 \"，cmd 不认识，命令就被破坏了。
func TestWindowsShellKeepsQuotes(t *testing.T) {
	r := runShell(t, `echo a b | findstr /c:"a b"`)
	if r.IsError || !strings.Contains(r.Output, "a b") {
		t.Fatalf("%+v", r)
	}
	r = runShell(t, `powershell -NoProfile -Command "Write-Output ('x' + 'y')"`)
	if r.IsError || !strings.Contains(r.Output, "xy") {
		t.Fatalf("PowerShell 的命令应被执行，而不是原样打印: %+v", r)
	}
}

// 错误信息（系统语言，中文系统是 GBK）必须是可读的 UTF-8，不能是乱码。
func TestWindowsShellOutputIsUTF8(t *testing.T) {
	r := runShell(t, `dir C:\this\path\does\not\exist`)
	if !utf8.ValidString(r.Output) || strings.ContainsRune(r.Output, '\ufffd') {
		t.Fatalf("输出应是合法 UTF-8: %q", r.Output)
	}
}
