package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectContext(t *testing.T) {
	ws := t.TempDir()
	if ProjectContext(ws) != "" || ProjectContext("") != "" {
		t.Fatal("没有文件时应为空")
	}
	_ = os.MkdirAll(filepath.Join(ws, ".myllmapp"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, ".myllmapp", "context.md"), []byte("  用 pnpm 跑测试\n"), 0o644)
	if got := ProjectContext(ws); !strings.HasSuffix(got, "\n用 pnpm 跑测试") || !strings.Contains(got, ProjectContextFile) {
		t.Fatalf("got %q", got)
	}
	_ = os.WriteFile(filepath.Join(ws, ".myllmapp", "context.md"), []byte(strings.Repeat("字", maxProjectContext)), 0o644)
	if got := ProjectContext(ws); !strings.HasSuffix(got, "[truncated]") || len(got) > maxProjectContext+200 {
		t.Fatal("应截断")
	}
}
