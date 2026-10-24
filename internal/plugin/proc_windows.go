//go:build windows

package plugin

import (
	"os/exec"
	"syscall"
)

// hideWindow 避免桌面应用每次刷新状态栏都闪出一个控制台窗口。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
