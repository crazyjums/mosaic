//go:build windows

package builtin

import (
	"os/exec"

	"github.com/crazyjums/myllmapp/internal/sysproc"
)

// prepareCmd 在 Windows 上不为命令创建控制台窗口（见 internal/sysproc），取消时只结束 cmd 进程。
// 完整的进程树清理需要 Job Object，第一阶段不做。
func prepareCmd(cmd *exec.Cmd) {
	sysproc.HideConsole(cmd)
}
