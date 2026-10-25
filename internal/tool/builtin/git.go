package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// 只读的 git 工具：git_status、git_diff、git_log。
//
// 为什么不让模型直接用 shell 跑 git：shell 是 exec 风险，每次都要用户确认；而查看状态和 diff
// 是非常频繁的只读操作，单独做成 read 风险的工具可以免确认。参数是结构化的，不拼接命令行，
// 模型没法借机执行 `git push` 之类的写操作。

const gitTimeout = 30 * time.Second

// runGit 在工作区执行 git，返回输出。
func runGit(ctx context.Context, workspace string, maxBytes int, args ...string) tool.Result {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	// -c core.quotepath=off：中文文件名原样显示，而不是 "\346\226\207"。
	full := append([]string{"-c", "core.quotepath=off", "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = workspace
	prepareCmd(cmd)
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return tool.Errorf("无法运行 git：%v。请确认已安装 git", err)
		}
		msg := strings.TrimSpace(out.String())
		if strings.Contains(msg, "not a git repository") {
			return tool.Errorf("工作区不是 git 仓库")
		}
		return tool.Errorf("%s", truncate(msg, maxBytes))
	}
	s := strings.TrimRight(out.String(), "\n")
	if s == "" {
		s = "(no output)"
	}
	return tool.Result{Output: truncate(s, maxBytes)}
}

// GitStatus 显示工作区的 git 状态。
type GitStatus struct{ maxBytes int }

func (t *GitStatus) Spec() tool.Spec {
	return tool.Spec{
		Name:        "git_status",
		Description: "查看工作区 git 仓库的状态（当前分支、已修改/暂存/未跟踪的文件）。只读。",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Risk:        permission.RiskRead,
	}
}

func (t *GitStatus) Summarize(json.RawMessage) string { return "查看 git 状态" }

func (t *GitStatus) Execute(ctx context.Context, call tool.Call) tool.Result {
	return runGit(ctx, call.Workspace, t.maxBytes, "status", "--short", "--branch")
}

// GitDiff 显示改动。
type GitDiff struct{ maxBytes int }

type gitDiffInput struct {
	Path   string `json:"path"`
	Staged bool   `json:"staged"`
}

func (t *GitDiff) Spec() tool.Spec {
	return tool.Spec{
		Name:        "git_diff",
		Description: "查看 git 改动（默认是未暂存的改动，staged=true 查看已暂存的）。可以用 path 只看某个文件或目录。只读。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "只看这个文件或目录，相对于工作区根目录"},
    "staged": {"type": "boolean", "description": "查看已暂存（git add 过）的改动，默认 false"}
  }
}`),
		Risk: permission.RiskRead,
	}
}

func (t *GitDiff) Summarize(input json.RawMessage) string {
	var in gitDiffInput
	_ = json.Unmarshal(input, &in)
	s := "查看 git diff"
	if in.Staged {
		s += "（已暂存）"
	}
	if in.Path != "" {
		s += " " + in.Path
	}
	return s
}

func (t *GitDiff) Execute(ctx context.Context, call tool.Call) tool.Result {
	var in gitDiffInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	args := []string{"diff"}
	if in.Staged {
		args = append(args, "--cached")
	}
	if in.Path != "" {
		// 路径也要限制在工作区内；"--" 之后的参数 git 只当作路径，不会被解析成选项。
		p, err := resolvePath(call.Workspace, in.Path)
		if err != nil {
			return tool.Errorf("%v", err)
		}
		args = append(args, "--", p)
	}
	return runGit(ctx, call.Workspace, t.maxBytes, args...)
}

// GitLog 显示最近的提交。
type GitLog struct{ maxBytes int }

type gitLogInput struct {
	N int `json:"n"`
}

func (t *GitLog) Spec() tool.Spec {
	return tool.Spec{
		Name:        "git_log",
		Description: "查看最近 n 个提交（默认 10，最多 100）：哈希、作者、日期和标题。只读。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {"n": {"type": "integer", "description": "提交数量，默认 10"}}
}`),
		Risk: permission.RiskRead,
	}
}

func (t *GitLog) Summarize(input json.RawMessage) string {
	var in gitLogInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("查看最近 %d 个提交", gitLogN(in.N))
}

func gitLogN(n int) int {
	if n <= 0 {
		return 10
	}
	return min(n, 100)
}

func (t *GitLog) Execute(ctx context.Context, call tool.Call) tool.Result {
	var in gitLogInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	return runGit(ctx, call.Workspace, t.maxBytes, "log", fmt.Sprintf("-%d", gitLogN(in.N)), "--date=short", "--pretty=format:%h %ad %an  %s")
}
