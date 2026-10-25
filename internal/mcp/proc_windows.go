//go:build windows

package mcp

import (
	"os/exec"

	"github.com/crazyjums/myllmapp/internal/sysproc"
)

// prepareProc 不为 server 创建控制台窗口（见 internal/sysproc）。
// 完整的进程树清理需要 Job Object，暂不做（与 shell 工具一致）。
func prepareProc(cmd *exec.Cmd) {
	sysproc.HideConsole(cmd)
}

func killProc(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
