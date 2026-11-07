package app

import (
	"context"
	"errors"
)

// Host 是桌面壳提供给绑定层的能力：给前端推事件、在系统浏览器里打开链接、系统对话框、退出应用。
//
// 为什么抽成接口：这几件事必须由“窗口”来做，而窗口属于哪个壳（Wails，或者 Electron 主进程）不应该影响
// 绑定层和内核。Wails 的实现在仓库根的 main 包（wails_host.go）；Electron 版里由 cmd/mosaicd 把它们
// 转成发给 Electron 主进程的 host.* 消息（见 docs/design/electron-migration.md）。
// 本包因此不再 import Wails，可以编进没有图形界面依赖的程序里。
type Host interface {
	// Emit 把一个事件推给前端（name 目前只有 EventName）。
	Emit(name string, data any)
	// OpenURL 在系统浏览器里打开链接。调用方已经校验过地址。
	OpenURL(url string)
	// OpenFile / OpenDirectory / SaveFile 弹出系统对话框，用户取消时返回空串。
	OpenFile(ctx context.Context, opts OpenFileOptions) (string, error)
	OpenDirectory(ctx context.Context, opts OpenDirectoryOptions) (string, error)
	SaveFile(ctx context.Context, opts SaveFileOptions) (string, error)
	// Quit 退出应用（自更新装好新版本、新进程已启动之后调用）。
	Quit()
}

// FileFilter 是对话框里的一个文件类型过滤器，例如 {"CSV (*.csv)", "*.csv"}。
type FileFilter struct {
	DisplayName string `json:"display_name"`
	Pattern     string `json:"pattern"`
}

// OpenFileOptions 是“选择文件”对话框的参数。
type OpenFileOptions struct {
	Title            string       `json:"title"`
	DefaultDirectory string       `json:"default_directory,omitempty"`
	Filters          []FileFilter `json:"filters,omitempty"`
}

// OpenDirectoryOptions 是“选择文件夹”对话框的参数。
type OpenDirectoryOptions struct {
	Title                string `json:"title"`
	DefaultDirectory     string `json:"default_directory,omitempty"`
	CanCreateDirectories bool   `json:"can_create_directories,omitempty"`
}

// SaveFileOptions 是“另存为”对话框的参数。
type SaveFileOptions struct {
	Title           string       `json:"title"`
	DefaultFilename string       `json:"default_filename,omitempty"`
	Filters         []FileFilter `json:"filters,omitempty"`
}

// ErrNoHost 表示当前没有桌面壳（例如测试里），需要窗口的操作做不了。
var ErrNoHost = errors.New("当前环境没有桌面窗口，无法打开系统对话框")

// SetHost 注入桌面壳。没有注入时：事件只交给 New 传入的 emit（测试里的记录函数），打开链接和退出什么也不做，
// 对话框返回 ErrNoHost。
func (a *App) SetHost(h Host) {
	a.hostMu.Lock()
	a.host = h
	a.hostMu.Unlock()
}

func (a *App) currentHost() Host {
	a.hostMu.Lock()
	defer a.hostMu.Unlock()
	return a.host
}

func (a *App) openURL(u string) {
	if h := a.currentHost(); h != nil && u != "" {
		h.OpenURL(u)
	}
}
