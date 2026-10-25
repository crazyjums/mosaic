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
	"unicode/utf8"

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
	// 工作区之外的绝对路径。用 filepath 构造，而不是写死 /etc/passwd：
	// 在 Windows 上 /etc/passwd 不是绝对路径（会被当成工作区内的相对路径），C:\... 才是。
	outside, _ := json.Marshal(map[string]string{"path": filepath.Join(filepath.Dir(ws), "outside.txt")})
	for _, in := range []string{
		`{"path":"../x"}`,
		string(outside),
		`{"path":"a/../../x"}`,
	} {
		if r := run(t, reg, ws, "read_file", in); !r.IsError || !strings.Contains(r.Output, "outside the workspace") {
			t.Errorf("%s 应被拒绝: %+v", in, r)
		}
	}
}

// 工作区里指向外部的符号链接不能被用来读写工作区之外的文件。
func TestSymlinkEscapeRejected(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Skipf("无法创建符号链接（Windows 需要开发者模式）: %v", err)
	}
	// 有的环境（例如 Wine）里 Symlink 返回成功却没有真正创建链接，此时测试没有意义。
	if fi, err := os.Lstat(filepath.Join(ws, "link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Skip("当前环境不支持符号链接")
	}
	reg := newRegistry()
	for name, in := range map[string]string{
		"read_file":  `{"path":"link/secret.txt"}`,
		"write_file": `{"path":"link/new/x.txt","content":"x"}`,
		"list_dir":   `{"path":"link"}`,
	} {
		if r := run(t, reg, ws, name, in); !r.IsError || !strings.Contains(r.Output, "outside the workspace") {
			t.Errorf("%s %s 应被拒绝: %+v", name, in, r)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatal("不应在工作区之外创建文件")
	}

	// 悬空链接（目标还不存在）也不能被用来在外部创建文件。
	target := filepath.Join(outside, "created-via-dangling.txt")
	if err := os.Symlink(target, filepath.Join(ws, "dangling")); err != nil {
		t.Fatal(err)
	}
	if r := run(t, reg, ws, "write_file", `{"path":"dangling","content":"x"}`); !r.IsError {
		t.Fatalf("经悬空链接写文件应被拒绝: %+v", r)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("不应经悬空链接在工作区之外创建文件")
	}

	// 指向工作区内部的链接仍然可用。
	if err := os.Mkdir(filepath.Join(ws, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "real"), filepath.Join(ws, "inner")); err != nil {
		t.Fatal(err)
	}
	if r := run(t, reg, ws, "write_file", `{"path":"inner/a.txt","content":"ok"}`); r.IsError {
		t.Fatalf("工作区内的链接应可用: %+v", r)
	}
}

func TestTruncateKeepsRunes(t *testing.T) {
	out := truncate("汉字汉字", 4) // 每个汉字 3 字节，4 字节处落在第二个字中间
	if !strings.HasPrefix(out, "汉\n") || !utf8.ValidString(out) {
		t.Fatalf("应在字符边界截断: %q", out)
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
	if len(reg.Specs()) != 15 {
		t.Fatal("应注册 15 个内置工具")
	}
}
