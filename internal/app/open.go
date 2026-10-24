package app

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openInFileManager 用系统的文件管理器打开一个目录（Finder / 资源管理器 / Linux 桌面的默认文件管理器）。
//
// 这是纯粹的桌面集成，与内核无关，所以放在绑定层。没有用 Wails 的 BrowserOpenURL("file://...")：
// 它在不同平台上的行为不一致（有的会用浏览器打开目录列表）。
func openInFileManager(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法打开文件管理器：%w", err)
	}
	// 不等待退出：文件管理器可能一直运行。用 goroutine 回收子进程，避免留下僵尸进程。
	go cmd.Wait()
	return nil
}
