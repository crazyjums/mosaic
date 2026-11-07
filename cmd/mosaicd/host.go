package main

import (
	"context"

	"github.com/crazyjums/myllmapp/internal/app"
	"github.com/crazyjums/myllmapp/internal/rpc"
)

// host 把 app.Host 的能力转成发给 Electron 主进程的消息：事件和打开链接、退出是通知（不等回应），
// 对话框是反向请求（等用户在对话框里选完）。消息名见 docs/design/electron-migration.md §4.3。
type host struct{ peer *rpc.Peer }

func (h *host) Emit(name string, data any) {
	_ = h.peer.Notify("event", map[string]any{"name": name, "data": data})
}

func (h *host) OpenURL(url string) { _ = h.peer.Notify("host.openURL", map[string]string{"url": url}) }

func (h *host) OpenFile(ctx context.Context, o app.OpenFileOptions) (string, error) {
	return h.ask(ctx, "host.openFile", o)
}

func (h *host) OpenDirectory(ctx context.Context, o app.OpenDirectoryOptions) (string, error) {
	return h.ask(ctx, "host.openDirectory", o)
}

func (h *host) SaveFile(ctx context.Context, o app.SaveFileOptions) (string, error) {
	return h.ask(ctx, "host.saveFile", o)
}

// InstallUpdate 请 Electron 主进程运行刚下载并校验过的安装包，随后它会退出并重启应用（app.UpdateInstaller）。
func (h *host) InstallUpdate(ctx context.Context, path string) error {
	return h.peer.Call(ctx, "host.installUpdate", map[string]string{"path": path}, nil)
}

func (h *host) Quit() { _ = h.peer.Notify("host.quit", struct{}{}) }

func (h *host) ask(ctx context.Context, method string, params any) (string, error) {
	var path string
	err := h.peer.Call(ctx, method, params, &path)
	return path, err
}
