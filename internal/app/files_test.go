package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newWS(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(ws, "out"), 0o755))
	must(os.WriteFile(filepath.Join(ws, "out", "report.html"), []byte("<h1>报告</h1>"), 0o644))
	must(os.WriteFile(filepath.Join(ws, "run.bat"), []byte("echo hi"), 0o644))
	must(os.WriteFile(filepath.Join(ws, "big.txt"), []byte(strings.Repeat("a", maxTextBytes+10)), 0o644))
	return ws
}

func TestReadWorkspaceText(t *testing.T) {
	ws := newWS(t)
	f, err := readWorkspaceText(ws, "out/report.html")
	if err != nil || f.Text != "<h1>报告</h1>" || !filepath.IsAbs(f.Path) || !strings.HasSuffix(f.Path, filepath.Join("out", "report.html")) {
		t.Fatalf("%v %+v", err, f)
	}
	// 工作区内的绝对路径也可以
	if _, err := readWorkspaceText(ws, filepath.Join(ws, "out", "report.html")); err != nil {
		t.Fatal(err)
	}
	if f, err := readWorkspaceText(ws, "big.txt"); err != nil || !f.Truncated || len(f.Text) != maxTextBytes {
		t.Fatalf("超大文件应截断: %v %v %d", err, f.Truncated, len(f.Text))
	}
	for _, bad := range []string{"run.bat", "../x.html", "out", "nope.html", ""} {
		if _, err := readWorkspaceText(ws, bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
	if _, err := readWorkspaceText("", "a.html"); err == nil {
		t.Error("没有工作区应报错")
	}
}

func TestReadWorkspaceTextRejectsSymlinkEscape(t *testing.T) {
	ws := newWS(t)
	outside := filepath.Join(t.TempDir(), "secret.html")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link.html")); err != nil {
		t.Skip("不能创建符号链接")
	}
	if _, err := readWorkspaceText(ws, "link.html"); err == nil {
		t.Fatal("指到工作区外的符号链接应被拒绝")
	}
	if err := openWorkspaceFile(ws, "link.html", func(string) error { t.Fatal("不应打开"); return nil }); err == nil {
		t.Fatal("指到工作区外的符号链接应被拒绝")
	}
}

func TestOpenWorkspaceFile(t *testing.T) {
	ws := newWS(t)
	var opened string
	if err := openWorkspaceFile(ws, "out/report.html", func(p string) error { opened = p; return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(opened, filepath.Join("out", "report.html")) || !filepath.IsAbs(opened) {
		t.Fatalf("应以绝对路径打开: %q", opened)
	}
	// 可能被系统当作程序执行的类型一律不打开；工作区外、不存在的也不行。
	for _, bad := range []string{"run.bat", "a.exe", "x.lnk", "s.sh", "../out/report.html", "nope.html"} {
		called := false
		if err := openWorkspaceFile(ws, bad, func(string) error { called = true; return nil }); err == nil || called {
			t.Errorf("%q 不应被打开", bad)
		}
	}
}
