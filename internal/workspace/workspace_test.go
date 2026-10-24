package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInspectNotRepo(t *testing.T) {
	dir := t.TempDir()
	info := Inspect(context.Background(), dir)
	if info.IsRepo || info.Name != filepath.Base(dir) {
		t.Fatalf("非 git 目录: %+v", info)
	}
}

func TestInspectRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有安装 git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "init")

	info := Inspect(context.Background(), dir)
	if !info.IsRepo || info.Branch != "main" || info.Dirty {
		t.Fatalf("干净的仓库: %+v", info)
	}
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed"), 0o644)
	if info := Inspect(context.Background(), dir); !info.Dirty || info.ChangedFiles != 2 {
		t.Fatalf("有改动的仓库: %+v", info)
	}
}
