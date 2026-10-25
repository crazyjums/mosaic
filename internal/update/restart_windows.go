//go:build windows

package update

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

const detachedProcess = 0x00000008

// prepareRestart 让新进程脱离当前进程，不继承控制台。
func prepareRestart(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}

func waitExit(pid int, stop <-chan time.Time) {
	// Windows 上 FindProcess 会打开进程句柄（进程已不存在则报错），Wait 等待句柄变为有信号。
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = p.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-stop:
	}
}
