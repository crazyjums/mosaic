package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// Shell 在工作区根目录执行一条 shell 命令，返回合并后的 stdout/stderr 和退出码。
//
// 这是同步执行：命令跑完才返回。长时间运行的命令（dev server、watch）应该交给
// 将来的后台任务机制（core.TaskManager，见 docs/roadmap.md），而不是在这里阻塞 agent 循环。
type Shell struct {
	// timeout 用原子变量保存：设置页修改超时时，可能有另一个 goroutine 正在读取它。
	timeout  atomic.Int64 // time.Duration
	maxBytes int
}

// NewShell 创建 shell 工具。
func NewShell(timeout time.Duration, maxBytes int) *Shell {
	s := &Shell{maxBytes: maxBytes}
	s.SetTimeout(timeout)
	return s
}

// SetTimeout 修改单条命令的超时，对之后的调用生效（设置 → 常规）。小于等于 0 时使用默认的 60 秒。
func (t *Shell) SetTimeout(d time.Duration) {
	if d <= 0 {
		d = DefaultShellTimeout
	}
	t.timeout.Store(int64(d))
}

// Timeout 返回当前的超时设置。
func (t *Shell) Timeout() time.Duration { return time.Duration(t.timeout.Load()) }

type shellInput struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Cwd            string `json:"cwd"`
}

func (t *Shell) Spec() tool.Spec {
	return tool.Spec{
		Name:        "shell",
		Description: fmt.Sprintf("执行一条 shell 命令（%s），返回输出和退出码。默认在工作区根目录执行、超时 %s；超时会返回已产生的输出。每次调用都是新进程，cd 不会保留到下一次（用 cwd 参数，或在同一条命令里用 & 连接）。不要运行需要交互或长期不退出的命令。读文件、搜索、改文件请优先用专门的工具。%s", shellName(), t.Timeout(), shellHint()),
		InputSchema: json.RawMessage(fmt.Sprintf(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "要执行的命令"},
    "timeout_seconds": {"type": "integer", "description": "超时秒数，默认 %d，最大 %d"},
    "cwd": {"type": "string", "description": "执行目录，相对于工作区根目录，默认 ."}
  },
  "required": ["command"]
}`, int(t.Timeout().Seconds()), int(MaxShellTimeout.Seconds()))),
		Risk: permission.RiskExec,
	}
}

func (t *Shell) Summarize(input json.RawMessage) string {
	var in shellInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("执行命令：%s", in.Command)
}

func (t *Shell) Execute(ctx context.Context, call tool.Call) tool.Result {
	var in shellInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	if in.Command == "" {
		return tool.Errorf("command is required")
	}
	if reason := dangerousCommand(in.Command); reason != "" {
		return tool.Errorf("已拒绝执行：%s。这类命令影响面太大，不允许由模型执行；如确有需要，请让用户在终端里手动运行", reason)
	}
	dir := call.Workspace
	if in.Cwd != "" {
		d, err := resolvePath(call.Workspace, in.Cwd)
		if err != nil {
			return tool.Errorf("cwd: %v", err)
		}
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			return tool.Errorf("cwd %s 不是存在的目录，建议先用 list_dir 确认", in.Cwd)
		}
		dir = d
	}

	// 超时与用户取消共用一个 ctx：任何一个触发，exec.CommandContext 都会杀掉进程。
	timeout := t.Timeout()
	if in.TimeoutSeconds > 0 {
		timeout = min(time.Duration(in.TimeoutSeconds)*time.Second, MaxShellTimeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := newShellCmd(ctx, in.Command)
	cmd.Dir = dir
	prepareCmd(cmd)
	// 进程被杀后最多再等 1 秒收尾输出，防止子进程继承的管道让 Wait 永远不返回。
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	output := truncate(decodeOutput(out.Bytes()), t.maxBytes)

	var exitErr *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// 超时时仍然返回已经产生的输出：模型往往能从中看出卡在哪一步。
		return tool.Errorf("%s\n[timed out after %s；以上是超时前的输出。需要更久可以调大 timeout_seconds（最大 %d），长期运行的服务请让用户自己启动]", output, timeout, int(MaxShellTimeout.Seconds()))
	case ctx.Err() != nil:
		return tool.Errorf("%s\n[cancelled]", output)
	case errors.As(err, &exitErr):
		return tool.Errorf("%s\n[exit code %d]", output, exitErr.ExitCode())
	case err != nil:
		return tool.Errorf("failed to run command: %v", err)
	}
	if output == "" {
		output = "(no output)"
	}
	return tool.Result{Output: output + "\n[exit code 0]"}
}
