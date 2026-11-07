package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/update"
)

// 客户端更新的绑定。逻辑都在 internal/update，这里只做三件事：
// 保存最近一次检查的结果（安装时要用它里面的下载地址和 sha256，不信任前端传来的任何地址）、
// 在后台下载并暴露进度（前端轮询 GetUpdateStatus）、安装完成后重启。

// UpdateStatus 是安装进度。
type UpdateStatus struct {
	// State：idle / downloading / ready（已下载并校验，等用户确认安装）/ installing / restarting / error
	State string `json:"state"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
	// Version 是 ready / installing / restarting 时对应的版本号。
	Version string `json:"version,omitempty"`
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
	// downloaded 是已经下载并校验好、等待安装的安装包路径（state 为 ready 时有值）。
	downloaded string
	// quit 在安装完成、新进程启动后调用；生产环境是 Host.Quit，测试里替换。
	quit func(ctx context.Context)
}

func (a *App) updater() *update.Updater {
	if a.upd == nil {
		a.upd = update.New()
	}
	return a.upd
}

// networkUpdater 为单次更新检查/下载复制 Updater 并配置最新全局代理。
// 不修改 a.upd 的共享指针，避免并发检查/下载或修改代理设置时引入数据竞争。
// 未配置应用代理时继续由 Go 默认 transport 使用 HTTP(S)_PROXY 环境变量。
func (a *App) networkUpdater() (*update.Updater, error) {
	u := *a.updater()
	proxyURL := ""
	if a.settings != nil {
		proxyURL = a.settings.UpdateProxyURL()
	}
	client, err := provider.NewHTTPClientWithProxy(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("更新代理配置错误：%w", err)
	}
	client.Timeout = 30 * time.Second // 清单请求；下载时 updater.Download 会去掉总超时
	u.Client = client
	return &u, nil
}

// GetVersion 返回当前版本号（开发构建是 "dev"）。
func (a *App) GetVersion() string { return a.updater().Current }

// CheckUpdate 检查是否有新版本。网络错误、签名校验失败等都以 error 返回，由前端决定是否提示。
func (a *App) CheckUpdate() (UpdateInfo, error) {
	base := a.updater()
	if ok, why := base.Enabled(); !ok {
		return UpdateInfo{Info: update.Info{Current: base.Current}, Disabled: why}, nil
	}
	u, err := a.networkUpdater()
	if err != nil {
		return UpdateInfo{Info: update.Info{Current: base.Current}}, err
	}
	info, err := u.Check(a.context())
	if err != nil {
		return UpdateInfo{Info: update.Info{Current: u.Current}}, err
	}
	a.us.mu.Lock()
	a.us.last = info
	// 已经下载好的是更旧的版本：丢掉它，免得用户点“安装”装上的不是最新版。
	if a.us.status.State == "ready" && a.us.status.Version != info.Latest {
		_ = os.Remove(a.us.downloaded)
		a.us.downloaded = ""
		a.us.status = UpdateStatus{State: "idle"}
	}
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

// 更新分两步，由前端决定什么时候走第二步：
//  1. DownloadUpdate：下载并校验安装包（不影响正在做的事，可以随时进行），完成后状态是 ready；
//  2. ApplyUpdate：安装并重启。agent 在运行时会被拒绝——重启会打断它，前端据此等这一轮结束再调用。

// DownloadUpdate 在后台下载最近一次检查到的新版本并校验。立即返回，进度用 GetUpdateStatus 轮询。
func (a *App) DownloadUpdate() error {
	a.us.mu.Lock()
	info := a.us.last
	st, readyVersion := a.us.status.State, a.us.status.Version
	a.us.mu.Unlock()
	switch {
	case st == "downloading" || st == "installing" || st == "restarting":
		return errors.New("正在更新中")
	case st == "ready" && info != nil && readyVersion == info.Latest:
		return nil // 已经下载好了
	case info == nil || !info.Available:
		return errors.New("请先检查更新")
	case info.Manual:
		return errors.New("这个平台需要手动下载安装")
	case a.shellUpdateDir != "" && !a.hostCanInstall():
		return errors.New("桌面壳不支持安装更新，请手动下载安装")
	}
	a.setUpdateStatus(UpdateStatus{State: "downloading", Total: info.Size, Version: info.Latest})
	go a.runDownload(info)
	return nil
}

func (a *App) runDownload(info *update.Info) {
	fail := func(err error) { a.setUpdateStatus(UpdateStatus{State: "error", Error: err.Error()}) }
	u, err := a.networkUpdater()
	if err != nil {
		fail(err)
		return
	}
	path, err := u.Download(a.context(), info, func(done, total int64) {
		a.setUpdateStatus(UpdateStatus{State: "downloading", Done: done, Total: total, Version: info.Latest})
	})
	if err != nil {
		fail(err)
		return
	}
	a.us.mu.Lock()
	a.us.downloaded = path
	a.us.status = UpdateStatus{State: "ready", Done: info.Size, Total: info.Size, Version: info.Latest}
	a.us.mu.Unlock()
}

// ApplyUpdate 安装已经下载好的新版本并重启应用。立即返回，安装在后台进行。
func (a *App) ApplyUpdate() error {
	a.us.mu.Lock()
	path, version, st := a.us.downloaded, a.us.status.Version, a.us.status.State
	a.us.mu.Unlock()
	switch {
	case st != "ready" || path == "":
		return errors.New("还没有下载好的更新")
	case a.engine.Status().Running:
		return errors.New("agent 正在运行，请等这一轮结束或先停止，再更新")
	}
	size := a.updateSize()
	a.setUpdateStatus(UpdateStatus{State: "installing", Done: size, Total: size, Version: version})
	go a.runApply(path, version, size)
	return nil
}

func (a *App) updateSize() int64 {
	a.us.mu.Lock()
	defer a.us.mu.Unlock()
	return a.us.status.Total
}

func (a *App) runApply(path, version string, size int64) {
	fail := func(err error) {
		// 安装失败：安装包还在，回到 ready 并带上原因，用户可以重试，也可以手动安装。
		a.us.mu.Lock()
		a.us.status = UpdateStatus{State: "ready", Done: size, Total: size, Version: version, Error: err.Error()}
		a.us.mu.Unlock()
	}
	u, err := a.networkUpdater()
	if err != nil {
		fail(err)
		return
	}
	if a.shellUpdateDir != "" {
		// 安装包已经按签名清单里的 sha256 校验过；运行安装程序、退出并重启由壳完成。
		inst, _ := a.currentHost().(UpdateInstaller)
		if inst == nil {
			fail(errors.New("桌面壳不支持安装更新，请手动下载安装"))
			return
		}
		if err := inst.InstallUpdate(a.context(), path); err != nil {
			fail(err)
			return
		}
		a.setUpdateStatus(UpdateStatus{State: "restarting", Done: size, Total: size, Version: version})
		return
	}
	if err := u.Apply(path); err != nil {
		fail(err)
		return
	}
	a.setUpdateStatus(UpdateStatus{State: "restarting", Done: size, Total: size, Version: version})
	if err := u.Restart(); err != nil {
		fail(errors.New("新版本已安装，但自动重启失败，请手动重新打开应用: " + err.Error()))
		return
	}
	// 新进程已经启动，它会等本进程退出后再初始化（update.WaitForPrevious）。
	quit := a.us.quit
	if quit == nil {
		quit = func(context.Context) {
			if h := a.currentHost(); h != nil {
				h.Quit()
			}
		}
	}
	quit(a.context())
}

// InstallUpdate 下载并立即安装（等于 DownloadUpdate + ApplyUpdate）。保留给命令行和旧的调用方；
// 界面走两步，以便下载完成后先问用户。
func (a *App) InstallUpdate() error {
	if err := a.DownloadUpdate(); err != nil {
		return err
	}
	if a.engine.Status().Running {
		return errors.New("agent 正在运行，请等这一轮结束或先停止，再更新")
	}
	go func() {
		for i := 0; i < 600; i++ { // 最多等 5 分钟下载
			a.us.mu.Lock()
			st := a.us.status.State
			a.us.mu.Unlock()
			if st == "ready" {
				_ = a.ApplyUpdate()
				return
			}
			if st != "downloading" {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
	return nil
}

// SetChangelog 交给 CHANGELOG.md 的原文（由 main 用 go:embed 打包进程序）。
// 版本历史直接从程序自带的日志解析：离线可用，也不依赖发布服务器上的清单带了几个版本。
func (a *App) SetChangelog(md string) { a.changelog = md }

// GetVersionHistory 返回程序自带的全部历史版本更新日志，最新的在前。
// [Unreleased] 不算：它还没发布，用户装不到。
func (a *App) GetVersionHistory() []update.Release { return update.ParseChangelog(a.changelog) }

// UpdateInstaller 是桌面壳可选的能力：运行已校验的安装包、退出并重启（Electron 版实现；Wails 版是自己替换自己）。
type UpdateInstaller interface {
	// InstallUpdate 的 path 是后端刚下载并校验过的安装包。成功返回后壳会很快退出本应用。
	InstallUpdate(ctx context.Context, path string) error
}

func (a *App) hostCanInstall() bool {
	_, ok := a.currentHost().(UpdateInstaller)
	return ok
}

// UseShellUpdater 让更新改由桌面壳安装（Electron 版）：Go 后端只是安装包里的一个子程序，不能覆盖自己；
// 它检查签名清单里 "<平台>-electron" 的安装包，下载到 dir 并校验 sha256，再交给壳（Host 的 UpdateInstaller）运行。
// 信任模型不变：只信任内置公钥签过的清单。必须在 Startup 之前调用。
func (a *App) UseShellUpdater(dir string) {
	a.shellUpdateDir = dir
	u := a.updater()
	u.PlatformSuffix = "-electron"
	u.DownloadDir = dir
}
