package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// Shell 在工作区根目录执行一条 shell 命令，返回合并后的 stdout/stderr 和退出码。
//
// 这是同步执行：命令跑完才返回。长时间运行的命令（dev server、watch）应该交给
// 将来的后台任务机制（core.TaskManager，见 docs/roadmap.md），而不是在这里阻塞 agent 循环。
type Shell struct {
	timeout  time.Duration
	maxBytes int
}

type shellInput struct {
	Command string `json:"command"`
}

func (t *Shell) Spec() tool.Spec {
	return tool.Spec{
		Name:        "shell",
		Description: fmt.Sprintf("在工作区根目录执行一条 shell 命令（%s），返回输出和退出码。超时 %s。不要运行需要交互或长期不退出的命令。", shellName(), t.timeout),
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "要执行的命令"}
  },
  "required": ["command"]
}`),
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

	// 超时与用户取消共用一个 ctx：任何一个触发，exec.CommandContext 都会杀掉进程。
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	name, flag := shellName(), "-c"
	if runtime.GOOS == "windows" {
		flag = "/C"
	}
	cmd := exec.CommandContext(ctx, name, flag, in.Command)
	cmd.Dir = call.Workspace
	prepareCmd(cmd)
	// 进程被杀后最多再等 1 秒收尾输出，防止子进程继承的管道让 Wait 永远不返回。
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	output := truncate(out.String(), t.maxBytes)

	var exitErr *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return tool.Errorf("%s\n[timed out after %s]", output, t.timeout)
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

func shellName() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}
