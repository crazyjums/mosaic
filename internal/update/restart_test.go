//go:build !windows

package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Restart 要启动新进程，并把当前 pid 通过环境变量传给它。
func TestRestartPassesPid(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	exe := filepath.Join(dir, "app")
	_ = os.WriteFile(exe, []byte("#!/bin/sh\necho \"$"+RestartEnv+"\" > "+marker+"\n"), 0o755)
	u := &Updater{ExePath: exe}
	if err := u.Restart(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == strconv.Itoa(os.Getpid()) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("新进程没有收到旧进程的 pid")
}

// 新进程等旧进程退出再继续；没有环境变量时立即返回；超时也会返回。
func TestWaitForPrevious(t *testing.T) {
	start := time.Now()
	WaitForPrevious(time.Second)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("没有设置环境变量时不应等待")
	}

	cmd := exec.Command("sleep", "0.4")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(RestartEnv, strconv.Itoa(cmd.Process.Pid))
	start = time.Now()
	go cmd.Wait() // 回收子进程，否则它会成为僵尸、信号 0 仍然成功
	WaitForPrevious(5 * time.Second)
	if d := time.Since(start); d < 300*time.Millisecond || d > 3*time.Second {
		t.Fatalf("应等到旧进程退出，实际等了 %v", d)
	}

	long := exec.Command("sleep", "30")
	_ = long.Start()
	defer func() { _ = long.Process.Kill(); _ = long.Wait() }()
	t.Setenv(RestartEnv, strconv.Itoa(long.Process.Pid))
	start = time.Now()
	WaitForPrevious(300 * time.Millisecond)
	if time.Since(start) > 2*time.Second {
		t.Fatal("超时后应放弃等待")
	}
}
