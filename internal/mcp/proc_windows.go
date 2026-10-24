//go:build windows

package mcp

import (
	"os/exec"
	"syscall"
)

// prepareProc 隐藏控制台窗口：桌面应用启动命令行程序时，Windows 默认会弹出一个黑色窗口。
// 完整的进程树清理需要 Job Object，暂不做（与 shell 工具一致）。
func prepareProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func killProc(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
