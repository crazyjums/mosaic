//go:build desktop || dev || production || bindings

package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/crazyjums/myllmapp/internal/app"
)

// wailsHost 用 Wails 的 runtime 实现 app.Host（推事件、打开链接、系统对话框、退出）。
// 放在 main 包而不是 internal/app：只有 Wails 入口需要它，绑定层因此不依赖 Wails，Electron 版可以复用同一个 internal/app。
type wailsHost struct{ ctx context.Context }

func (h wailsHost) Emit(name string, data any) { runtime.EventsEmit(h.ctx, name, data) }

func (h wailsHost) OpenURL(url string) { runtime.BrowserOpenURL(h.ctx, url) }

func (h wailsHost) OpenFile(_ context.Context, o app.OpenFileOptions) (string, error) {
	return runtime.OpenFileDialog(h.ctx, runtime.OpenDialogOptions{Title: o.Title, DefaultDirectory: o.DefaultDirectory, Filters: filters(o.Filters)})
}

func (h wailsHost) OpenDirectory(_ context.Context, o app.OpenDirectoryOptions) (string, error) {
	return runtime.OpenDirectoryDialog(h.ctx, runtime.OpenDialogOptions{Title: o.Title, DefaultDirectory: o.DefaultDirectory, CanCreateDirectories: o.CanCreateDirectories})
}

func (h wailsHost) SaveFile(_ context.Context, o app.SaveFileOptions) (string, error) {
	return runtime.SaveFileDialog(h.ctx, runtime.SaveDialogOptions{Title: o.Title, DefaultFilename: o.DefaultFilename, Filters: filters(o.Filters)})
}

func (h wailsHost) Quit() { runtime.Quit(h.ctx) }

func filters(in []app.FileFilter) []runtime.FileFilter {
	out := make([]runtime.FileFilter, 0, len(in))
	for _, f := range in {
		out = append(out, runtime.FileFilter{DisplayName: f.DisplayName, Pattern: f.Pattern})
	}
	return out
}
