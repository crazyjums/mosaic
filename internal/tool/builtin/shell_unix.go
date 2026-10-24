//go:build !windows

package builtin

import (
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
