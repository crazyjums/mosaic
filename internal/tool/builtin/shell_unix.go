//go:build !windows

package builtin

import (
	"context"
	"os/exec"
	"syscall"
)

// prepareCmd 让命令运行在独立的进程组里，取消时杀掉整个进程组。
// 否则 `sh -c "sleep 100"` 被取消时只杀掉 sh，sleep 会变成孤儿进程并继续占用输出管道。
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// newShellCmd 用 sh -c 运行命令。
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}

// decodeOutput：类 Unix 系统的命令输出就是 UTF-8。
func decodeOutput(b []byte) string { return string(b) }

func shellName() string { return "sh" }

// shellHint 是追加在工具描述后面的、这个系统上特有的提醒。
func shellHint() string { return "" }
