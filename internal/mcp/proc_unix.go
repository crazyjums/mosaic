//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

// prepareProc 让 server 运行在独立的进程组里，关闭时杀掉整个进程组。
// 常见的 `npx some-server` 会再启动一个 node 子进程，只杀 npx 会留下孤儿进程。
func prepareProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProc(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
