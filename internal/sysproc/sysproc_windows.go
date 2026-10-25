//go:build windows

package sysproc

import (
	"os/exec"
	"syscall"
)

// createNoWindow 是 Win32 的 CREATE_NO_WINDOW 进程创建标志（syscall 包没有导出这个常量）。
const createNoWindow = 0x08000000

// HideConsole 让 cmd 启动时不创建控制台窗口。保留调用方已经设置的其他 SysProcAttr 字段。
func HideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
