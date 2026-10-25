//go:build !windows

package sysproc

import "os/exec"

// HideConsole 在 Windows 以外的平台上什么都不做：那里启动命令行程序不会弹出窗口。
func HideConsole(*exec.Cmd) {}
