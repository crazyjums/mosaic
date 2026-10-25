package app

import (
	"fmt"
	"os"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/settings"
)

// 用量统计、模型组（故障转移）、网络代理的绑定。逻辑都在 internal/settings、internal/usage 和 internal/provider。

// GetUsageReport 返回用量报表（花费是美元，人民币由界面按用户填的汇率换算）。
func (a *App) GetUsageReport(req settings.UsageRequest) (settings.UsageReportView, error) {
	return a.settings.UsageReport(a.context(), req)
}

// SaveUsageCSV 弹出"另存为"对话框，把范围内的原始记录导出为 CSV。返回保存的路径；用户取消时返回空串。
func (a *App) SaveUsageCSV(req settings.UsageRequest) (string, error) {
	csv, err := a.settings.ExportUsageCSV(a.context(), req)
	if err != nil {
		return "", err
	}
	path, err := runtime.SaveFileDialog(a.context(), runtime.SaveDialogOptions{
		Title:           "导出用量记录",
		DefaultFilename: fmt.Sprintf("mosaic-usage-%s.csv", time.Now().Format("2006-01-02")),
		Filters:         []runtime.FileFilter{{DisplayName: "CSV (*.csv)", Pattern: "*.csv"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	// 带 UTF-8 BOM：Excel 直接双击打开 CSV 时靠它识别中文。
	if err := os.WriteFile(path, append([]byte("\xef\xbb\xbf"), csv...), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// GetGroups 返回所有模型组。
func (a *App) GetGroups() []settings.GroupView { return nonNilGroups(a.settings.Groups()) }

func nonNilGroups(g []settings.GroupView) []settings.GroupView {
	if g == nil {
		return []settings.GroupView{}
	}
	return g
}

// SaveGroup 新建或修改一个模型组。
func (a *App) SaveGroup(in settings.GroupInput) error { return a.settings.SaveGroup(in) }

// DeleteGroup 删除一个模型组。
func (a *App) DeleteGroup(name string) error { return a.settings.DeleteGroup(name) }

// GetGroupStatuses 返回各模型组成员的运行状态。设置页打开时每隔几秒轮询一次。
func (a *App) GetGroupStatuses() []provider.GroupStatus {
	st := a.settings.GroupStatuses()
	if st == nil {
		return []provider.GroupStatus{}
	}
	return st
}

// ResetGroupMember 手动恢复一个成员。
func (a *App) ResetGroupMember(profile, model string) { a.settings.ResetMember(profile, model) }

// GetProxy 返回全局默认代理（密码已脱敏）。
func (a *App) GetProxy() settings.ProxyView { return a.settings.Proxy() }

// SetProxy 保存全局默认代理；空串表示不设置。
func (a *App) SetProxy(raw string) error { return a.settings.SetProxy(raw) }
