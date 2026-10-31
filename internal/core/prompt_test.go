package core

import (
	"os"
	"path/filepath"
	"runtime"
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

// 模型要知道 shell 工具用的是什么 shell，以及每次调用是新进程（cd 不保留）。
func TestDefaultSystemPromptSaysWhichShell(t *testing.T) {
	p := DefaultSystemPrompt("/ws")
	if !strings.Contains(p, "Shell:") || !strings.Contains(p, "cd does not persist") || !strings.Contains(p, "cwd") {
		t.Fatalf("提示词里应说明 shell：%s", p)
	}
	if runtime.GOOS == "windows" && !strings.Contains(p, "cmd.exe") {
		t.Fatal("Windows 上应说明是 cmd.exe")
	}
}
