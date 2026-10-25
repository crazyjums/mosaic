package workspace

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/sysproc"
)

// Info 是状态栏需要的工作区信息。
type Info struct {
	Path string `json:"path"`
	// Name 是工作区目录名，状态栏默认只显示它。
	Name   string `json:"name"`
	IsRepo bool   `json:"is_repo"`
	// Branch 是当前分支；处于 detached HEAD 时是短提交号。
	Branch string `json:"branch,omitempty"`
	// Dirty 表示有未提交的改动（包括未跟踪的文件），ChangedFiles 是改动的文件数。
	Dirty        bool `json:"dirty"`
	ChangedFiles int  `json:"changed_files"`
}

// Inspect 读取工作区信息。任何 git 错误都只会让 IsRepo=false，不返回 error——
// 状态栏少显示一项不应该打扰用户。
func Inspect(ctx context.Context, dir string) Info {
	info := Info{Path: dir, Name: filepath.Base(dir)}
	// git 偶尔会因为大仓库或锁文件变慢，状态栏不值得等太久。
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	branch, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return info
	}
	info.IsRepo = true
	info.Branch = branch
	if branch == "HEAD" { // detached HEAD：显示短提交号
		if sha, err := git(ctx, dir, "rev-parse", "--short", "HEAD"); err == nil {
			info.Branch = sha
		}
	}
	if out, err := git(ctx, dir, "status", "--porcelain"); err == nil && out != "" {
		info.Dirty = true
		info.ChangedFiles = len(strings.Split(out, "\n"))
	}
	return info
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	sysproc.HideConsole(cmd) // 状态栏每 15 秒调用一次：在 Windows 上不隐藏会周期性地闪出黑色窗口
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}
