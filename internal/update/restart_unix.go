//go:build !windows

package update

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// prepareRestart 让新进程脱离当前进程组：旧进程退出时不会连带它一起被终止。
func prepareRestart(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func waitExit(pid int, stop <-chan time.Time) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	for {
		// 信号 0 不发送任何信号，只检查进程是否还存在。
		if p.Signal(syscall.Signal(0)) != nil {
			return
		}
		select {
		case <-stop:
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
