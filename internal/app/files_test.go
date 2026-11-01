package app

import (
	"os"
	"encoding/binary"
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
	if f, err := readWorkspaceText(ws, "run.bat"); err != nil || f.Text != "echo hi" {
		t.Fatalf("脚本只读预览应允许: %+v %v", f, err)
	}
	for _, bad := range []string{"../x.html", "out", "nope.html", ""} {
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

func TestAutoModeAllowsExternalDocumentsButNotExecutables(t *testing.T) {
	ws := newWS(t)
	outside := t.TempDir()
	path := filepath.Join(outside, "report.html")
	if err := os.WriteFile(path, []byte("<p>outside</p>"), 0o644); err != nil { t.Fatal(err) }
	if _, err := readWorkspaceText(ws, path); err == nil { t.Fatal("default mode must reject outside paths") }
	f, err := readWorkspaceTextWithAccess(ws, path, true)
	if err != nil || f.Text != "<p>outside</p>" { t.Fatalf("auto read: %+v %v", f, err) }
	var opened string
	expected, err := filepath.EvalSymlinks(path)
	if err != nil { t.Fatal(err) }
	if err := openWorkspaceFileWithAccess(ws, path, true, func(p string) error { opened = p; return nil }); err != nil || opened != expected {
		t.Fatalf("auto open: %q %v", opened, err)
	}
	exe := filepath.Join(outside, "run.bat")
	if err := os.WriteFile(exe, []byte("echo hi"), 0o644); err != nil { t.Fatal(err) }
	if err := openWorkspaceFileWithAccess(ws, exe, true, func(string) error { t.Fatal("never execute"); return nil }); err == nil {
		t.Fatal("auto mode must not auto-execute programs from a link")
	}
}

func TestFileURLPath(t *testing.T) {
	if got := cleanPath("file:///tmp/a%20b.html"); got != filepath.FromSlash("/tmp/a b.html") {
		t.Fatalf("decoded file url: %q", got)
	}
}

func TestArtifactPreviewTextAndSafeOpen(t *testing.T) {
	ws := newWS(t)
	for name, body := range map[string]string{
		"generate_report.py": "print('hello')\n",
		"config.toml": "value = 42\n",
		"tools.ps1": "Write-Host hello\n",
		"build.sh": "echo hello\n",
		"no-extension": "hello\n",
	} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(body), 0o644); err != nil { t.Fatal(err) }
		got, err := readWorkspaceText(ws, name)
		if err != nil || got.Text != body { t.Fatalf("%s should preview safely: %+v %v", name, got, err) }
	}

	var target, action string
	open := func(path string) error { action, target = "document", path; return nil }
	edit := func(path string) error { action, target = "editor", path; return nil }
	reveal := func(path string) error { action, target = "reveal", path; return nil }

	for file, expected := range map[string]string{
		"generate_report.py": "editor", "config.toml": "editor", "tools.ps1": "editor",
		"build.sh": "editor", "no-extension": "editor", "out/report.html": "document",
		"run.bat": "editor",
	} {
		action = ""
		if err := openArtifactWithAccess(ws, file, false, open, edit, reveal); err != nil { t.Fatal(err) }
		if action != expected || !filepath.IsAbs(target) { t.Fatalf("%s => %s path %s, want %s", file, action, target, expected) }
	}

	// 不运行未知二进制和可执行文件：只能定位。
	for _, file := range []string{"bad.exe", "binary.dat"} {
		if err := os.WriteFile(filepath.Join(ws, file), []byte{0, 1, 2, 3}, 0o644); err != nil { t.Fatal(err) }
		action = ""
		if err := openArtifactWithAccess(ws, file, false, open, edit, reveal); err != nil || action != "reveal" {
			t.Fatalf("%s must only reveal, got %s, %v", file, action, err)
		}
		if _, err := readWorkspaceText(ws, file); err == nil { t.Fatalf("%s must not preview binary", file) }
	}
}

func TestUTF16TextPreview(t *testing.T) {
	ws := newWS(t)
	file := filepath.Join(ws, "powershell_output.txt")
	expected := "已生成报告"
	encoded := []byte{0xff, 0xfe}
	for _, r := range expected {
		var pair [2]byte
		binary.LittleEndian.PutUint16(pair[:], uint16(r))
		encoded = append(encoded, pair[:]...)
	}
	if err := os.WriteFile(file, encoded, 0o644); err != nil { t.Fatal(err) }
	out, err := readWorkspaceText(ws, file)
	if err != nil || out.Text != expected { t.Fatalf("UTF-16 should decode: %+v %v", out, err) }
}

func TestArtifactRevealRespectsWorkspaceBoundary(t *testing.T) {
	ws := newWS(t)
	outside := filepath.Join(t.TempDir(), "external.py")
	if err := os.WriteFile(outside, []byte("print(1)"), 0o644); err != nil { t.Fatal(err) }
	called := false
	reveal := func(string) error { called = true; return nil }
	if err := revealWorkspaceFileWithAccess(ws, outside, false, reveal); err == nil || called {
		t.Fatal("default mode may not reveal outside file")
	}
	if err := revealWorkspaceFileWithAccess(ws, outside, true, reveal); err != nil || !called {
		t.Fatalf("auto mode may reveal outside file: %v", err)
	}
}
