//go:build windows

package builtin

import "os/exec"

// prepareCmd 在 Windows 上使用默认行为（只结束 cmd 进程）。
// 完整的进程树清理需要 Job Object，第一阶段不做。
func prepareCmd(cmd *exec.Cmd) {}
