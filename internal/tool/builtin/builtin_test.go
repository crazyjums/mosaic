package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/tool"
)

func newRegistry() *tool.Registry {
	reg := tool.NewRegistry()
	Register(reg, Options{ShellTimeout: 2 * time.Second, MaxOutputBytes: 64})
	return reg
}

func run(t *testing.T, reg *tool.Registry, ws, name, input string) tool.Result {
	t.Helper()
	tl, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %s not registered", name)
	}
	return tl.Execute(context.Background(), tool.Call{ID: "c", Input: json.RawMessage(input), Workspace: ws})
}

func TestWriteReadList(t *testing.T) {
	ws := t.TempDir()
	reg := newRegistry()

	if r := run(t, reg, ws, "write_file", `{"path":"a/b.txt","content":"hello"}`); r.IsError {
		t.Fatalf("write: %s", r.Output)
	}
	if r := run(t, reg, ws, "read_file", `{"path":"a/b.txt"}`); r.IsError || r.Output != "hello" {
		t.Fatalf("read: %+v", r)
	}
	r := run(t, reg, ws, "list_dir", `{}`)
	if r.IsError || r.Output != "a/" {
		t.Fatalf("list: %+v", r)
	}
}

func TestPathEscapeRejected(t *testing.T) {
	ws := t.TempDir()
	reg := newRegistry()
	for _, in := range []string{
		`{"path":"../x"}`,
		`{"path":"/etc/passwd"}`,
		`{"path":"a/../../x"}`,
	} {
		if r := run(t, reg, ws, "read_file", in); !r.IsError || !strings.Contains(r.Output, "outside the workspace") {
			t.Errorf("%s 应被拒绝: %+v", in, r)
		}
	}
}

func TestReadTruncatesAndRejectsBinary(t *testing.T) {
	ws := t.TempDir()
	reg := newRegistry()
	_ = os.WriteFile(filepath.Join(ws, "big.txt"), []byte(strings.Repeat("x", 200)), 0o644)
	_ = os.WriteFile(filepath.Join(ws, "bin"), []byte{0xff, 0xfe, 0x00}, 0o644)

	if r := run(t, reg, ws, "read_file", `{"path":"big.txt"}`); !strings.Contains(r.Output, "truncated") {
		t.Errorf("应截断: %q", r.Output)
	}
	if r := run(t, reg, ws, "read_file", `{"path":"bin"}`); !r.IsError {
		t.Error("二进制文件应报错")
	}
}

func TestInvalidJSONIsToolError(t *testing.T) {
	reg := newRegistry()
	if r := run(t, reg, t.TempDir(), "write_file", `{not json`); !r.IsError {
		t.Fatal("非法参数应返回工具错误而不是 panic")
	}
}

func TestShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell 测试使用 POSIX 命令")
	}
	ws := t.TempDir()
	reg := newRegistry()

	r := run(t, reg, ws, "shell", `{"command":"echo hi"}`)
	if r.IsError || !strings.HasPrefix(r.Output, "hi") {
		t.Fatalf("echo: %+v", r)
	}
	r = run(t, reg, ws, "shell", `{"command":"exit 3"}`)
	if !r.IsError || !strings.Contains(r.Output, "exit code 3") {
		t.Fatalf("exit 3: %+v", r)
	}
	r = run(t, reg, ws, "shell", `{"command":"pwd"}`)
	if !strings.Contains(r.Output, filepath.Base(ws)) {
		t.Fatalf("应在工作区执行: %+v", r)
	}
}

func TestShellTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	reg := tool.NewRegistry()
	Register(reg, Options{ShellTimeout: 100 * time.Millisecond})
	r := run(t, reg, t.TempDir(), "shell", `{"command":"sleep 5"}`)
	if !r.IsError || !strings.Contains(r.Output, "timed out") {
		t.Fatalf("应超时: %+v", r)
	}
}

func TestSummaries(t *testing.T) {
	reg := newRegistry()
	w, _ := reg.Get("write_file")
	if s := w.Summarize(json.RawMessage(`{"path":"n.md","content":"abc"}`)); s != "写入 n.md（3 字节）" {
		t.Fatalf("summary: %q", s)
	}
	if len(reg.Specs()) != 4 {
		t.Fatal("应注册 4 个内置工具")
	}
}
