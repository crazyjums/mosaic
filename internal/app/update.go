package app

import (
	"context"
	"errors"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/crazyjums/myllmapp/internal/update"
)

// 客户端更新的绑定。逻辑都在 internal/update，这里只做三件事：
// 保存最近一次检查的结果（安装时要用它里面的下载地址和 sha256，不信任前端传来的任何地址）、
// 在后台下载并暴露进度（前端轮询 GetUpdateStatus）、安装完成后重启。

// UpdateStatus 是安装进度。
type UpdateStatus struct {
	// State：idle / downloading / installing / restarting / error
	State string `json:"state"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

// UpdateInfo 是检查结果加上"更新功能是否可用"。
type UpdateInfo struct {
	update.Info
	// Disabled 非空表示更新功能不可用（开发版 / 没有公钥），内容是原因。
	Disabled string `json:"disabled,omitempty"`
}

type updateState struct {
	mu     sync.Mutex
	last   *update.Info
	status UpdateStatus
	// quit 在安装完成、新进程启动后调用；生产环境是退出 Wails，测试里替换。
	quit func(ctx context.Context)
}

func (a *App) updater() *update.Updater {
	if a.upd == nil {
		a.upd = update.New()
	}
	return a.upd
}

// GetVersion 返回当前版本号（开发构建是 "dev"）。
func (a *App) GetVersion() string { return a.updater().Current }

// CheckUpdate 检查是否有新版本。网络错误、签名校验失败等都以 error 返回，由前端决定是否提示。
func (a *App) CheckUpdate() (UpdateInfo, error) {
	u := a.updater()
	if ok, why := u.Enabled(); !ok {
		return UpdateInfo{Info: update.Info{Current: u.Current}, Disabled: why}, nil
	}
	info, err := u.Check(a.context())
	if err != nil {
		return UpdateInfo{Info: update.Info{Current: u.Current}}, err
	}
	a.us.mu.Lock()
	a.us.last = info
	a.us.mu.Unlock()
	return UpdateInfo{Info: *info}, nil
}

// GetUpdateStatus 返回安装进度，前端在安装期间每隔半秒轮询一次。
func (a *App) GetUpdateStatus() UpdateStatus {
	a.us.mu.Lock()
	defer a.us.mu.Unlock()
	return a.us.status
}

func (a *App) setUpdateStatus(s UpdateStatus) {
	a.us.mu.Lock()
	a.us.status = s
	a.us.mu.Unlock()
}

// InstallUpdate 下载并安装最近一次检查到的新版本，然后重启应用。
// 立即返回，下载在后台进行；运行中的任务会被拒绝（重启会打断它）。
func (a *App) InstallUpdate() error {
	a.us.mu.Lock()
	info := a.us.last
	busy := a.us.status.State == "downloading" || a.us.status.State == "installing" || a.us.status.State == "restarting"
	a.us.mu.Unlock()
	switch {
	case busy:
		return errors.New("正在更新中")
	case info == nil || !info.Available:
		return errors.New("请先检查更新")
	case info.Manual:
		return errors.New("这个平台需要手动下载安装")
	case a.engine.Status().Running:
		return errors.New("agent 正在运行，请等这一轮结束或先停止，再更新")
	}
	a.setUpdateStatus(UpdateStatus{State: "downloading", Total: info.Size})
	go a.runInstall(info)
	return nil
}

func (a *App) runInstall(info *update.Info) {
	u := a.updater()
	fail := func(err error) { a.setUpdateStatus(UpdateStatus{State: "error", Error: err.Error()}) }

	path, err := u.Download(a.context(), info, func(done, total int64) {
		a.setUpdateStatus(UpdateStatus{State: "downloading", Done: done, Total: total})
	})
	if err != nil {
		fail(err)
		return
	}
	a.setUpdateStatus(UpdateStatus{State: "installing", Done: info.Size, Total: info.Size})
	if err := u.Apply(path); err != nil {
		fail(err)
		return
	}
	a.setUpdateStatus(UpdateStatus{State: "restarting", Done: info.Size, Total: info.Size})
	if err := u.Restart(); err != nil {
		fail(errors.New("新版本已安装，但自动重启失败，请手动重新打开应用: " + err.Error()))
		return
	}
	// 新进程已经启动，它会等本进程退出后再初始化（update.WaitForPrevious）。
	quit := a.us.quit
	if quit == nil {
		quit = runtime.Quit
	}
	quit(a.context())
}

// SetChangelog 交给 CHANGELOG.md 的原文（由 main 用 go:embed 打包进程序）。
// 版本历史直接从程序自带的日志解析：离线可用，也不依赖发布服务器上的清单带了几个版本。
func (a *App) SetChangelog(md string) { a.changelog = md }

// GetVersionHistory 返回程序自带的全部历史版本更新日志，最新的在前。
// [Unreleased] 不算：它还没发布，用户装不到。
func (a *App) GetVersionHistory() []update.Release { return update.ParseChangelog(a.changelog) }
