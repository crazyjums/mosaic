package update

import (
	"os"
	"os/exec"
	"strconv"
	"time"
)

// RestartEnv 传给新进程：值是旧进程的 pid，新进程启动时先等它退出（见 WaitForPrevious）。
// 为什么要等：新旧两个实例同时打开同一个 SQLite 数据库和设置文件，会互相干扰。
const RestartEnv = "MYLLMAPP_UPDATED_FROM"

// Restart 启动（已经替换好的）当前可执行文件的新进程。调用方随后应让当前进程退出。
func (u *Updater) Restart() error {
	exe, err := u.exePath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), RestartEnv+"="+strconv.Itoa(os.Getpid()))
	prepareRestart(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// WaitForPrevious 在新进程启动时调用：如果是更新重启的，等旧进程退出（最多 timeout）。
func WaitForPrevious(timeout time.Duration) {
	pid, err := strconv.Atoi(os.Getenv(RestartEnv))
	if err != nil || pid <= 0 {
		return
	}
	_ = os.Unsetenv(RestartEnv)
	waitExit(pid, time.After(timeout))
}
