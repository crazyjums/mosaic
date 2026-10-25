package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWorkspaceImage(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(filepath.Join(ws, "img"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, "img", "效果图.svg"), []byte("<svg/>"), 0o644)
	_ = os.WriteFile(filepath.Join(outside, "x.png"), []byte("p"), 0o644)
	_ = os.WriteFile(filepath.Join(ws, "a.txt"), []byte("t"), 0o644)

	got, err := readWorkspaceImage(ws, "img/效果图.svg")
	if err != nil || got != "data:image/svg+xml;base64,PHN2Zy8+" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := readWorkspaceImage(ws, filepath.Join(ws, "img", "效果图.svg")); err != nil {
		t.Fatal("工作区内的绝对路径应可用", err)
	}
	for _, p := range []string{"../" + filepath.Base(outside) + "/x.png", filepath.Join(outside, "x.png"), "a.txt", "nope.png"} {
		if _, err := readWorkspaceImage(ws, p); err == nil {
			t.Errorf("%s 应被拒绝", p)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "x.png"), filepath.Join(ws, "link.png")); err == nil {
		if _, err := readWorkspaceImage(ws, "link.png"); err == nil || !strings.Contains(err.Error(), "工作区内") {
			t.Error("经符号链接指向外部的图片应被拒绝")
		}
	}
}

func TestIsWebURL(t *testing.T) {
	for raw, want := range map[string]bool{"https://a.com/x": true, "http://a": true, "file:///etc/passwd": false, "javascript:alert(1)": false, "x.svg": false, "https://": false} {
		if isWebURL(raw) != want {
			t.Errorf("%s", raw)
		}
	}
}
