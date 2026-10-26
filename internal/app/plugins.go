package app

import (
	"errors"
	"os"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/crazyjums/myllmapp/internal/plugin"
	"github.com/crazyjums/myllmapp/internal/skill"
)

// 插件（见 internal/plugin）的界面绑定：这里只做转发，规则和校验都在 plugin.Host 里。

// SetPlugins 注入插件管理器（桌面端入口在创建 App 之后调用；没有注入时插件相关绑定返回“不可用”）。
func (a *App) SetPlugins(h *plugin.Host) { a.plugins = h }

var errNoPlugins = errors.New("插件系统不可用")

// pluginSkills 返回已启用插件提供的技能文件夹。
func (a *App) pluginSkills() []skill.Folder {
	if a.plugins == nil {
		return nil
	}
	return a.plugins.SkillFolders()
}

// ListPlugins 返回已安装的插件。
func (a *App) ListPlugins() []plugin.Info {
	if a.plugins == nil {
		return []plugin.Info{}
	}
	return a.plugins.List()
}

// PreviewPlugin 校验一个插件目录或 .zip 文件，返回安装前要给用户确认的内容；此时什么都没有生效。
func (a *App) PreviewPlugin(src string) (plugin.Preview, error) {
	if a.plugins == nil {
		return plugin.Preview{}, errNoPlugins
	}
	return a.plugins.Preview(src)
}

// PreviewPlugin 的来源也可以是 https:// 开头的 Git 地址（可在末尾用 #分支或标签 指定版本）。

// ListPluginCatalog 返回插件市场的列表（内置的加上上次刷新到的在线列表），马上返回，不联网。
func (a *App) ListPluginCatalog() []plugin.CatalogEntry {
	if a.plugins == nil {
		return []plugin.CatalogEntry{}
	}
	return a.plugins.Catalog()
}

// RefreshPluginCatalog 联网刷新在线列表；失败时返回错误，界面继续显示原来的列表。
func (a *App) RefreshPluginCatalog() ([]plugin.CatalogEntry, error) {
	if a.plugins == nil {
		return []plugin.CatalogEntry{}, errNoPlugins
	}
	return a.plugins.RefreshCatalog(a.context())
}

// PreviewCatalogPlugin 取市场里的一个插件，返回安装前要给用户确认的内容（和 PreviewPlugin 之后的流程相同）。
func (a *App) PreviewCatalogPlugin(id string) (plugin.Preview, error) {
	if a.plugins == nil {
		return plugin.Preview{}, errNoPlugins
	}
	return a.plugins.PreviewCatalog(id)
}

// ScaffoldPlugin 在 parentDir 下生成一个可以直接安装的最小插件模板，返回新目录的路径。
func (a *App) ScaffoldPlugin(parentDir, id string) (string, error) {
	return plugin.Scaffold(parentDir, id)
}

// InstallPlugin 安装确认过的插件。approved 是用户勾选的、同意按作者声明的风险等级运行的 MCP 服务名。
func (a *App) InstallPlugin(token string, approved []string) (plugin.Info, error) {
	if a.plugins == nil {
		return plugin.Info{}, errNoPlugins
	}
	return a.plugins.Install(token, approved)
}

// CancelPluginPreview 放弃一次安装。
func (a *App) CancelPluginPreview(token string) {
	if a.plugins != nil {
		a.plugins.CancelPreview(token)
	}
}

func (a *App) SetPluginEnabled(id string, on bool) (plugin.Info, error) {
	if a.plugins == nil {
		return plugin.Info{}, errNoPlugins
	}
	return a.plugins.SetEnabled(id, on)
}

// UninstallPlugin 卸载插件；purge 为 true 时一并清除配置、密钥和插件数据。
func (a *App) UninstallPlugin(id string, purge bool) error {
	if a.plugins == nil {
		return errNoPlugins
	}
	return a.plugins.Uninstall(id, purge)
}

// SavePluginConfig 保存插件的设置表单。secret 项为空串表示不修改。
func (a *App) SavePluginConfig(id string, values map[string]string) (plugin.Info, error) {
	if a.plugins == nil {
		return plugin.Info{}, errNoPlugins
	}
	return a.plugins.SetConfig(id, values)
}

func (a *App) ClearPluginSecret(id, key string) (plugin.Info, error) {
	if a.plugins == nil {
		return plugin.Info{}, errNoPlugins
	}
	return a.plugins.ClearSecret(id, key)
}

// GetPluginStatus 运行插件声明的状态检查。
func (a *App) GetPluginStatus(id string) (plugin.StatusResult, error) {
	if a.plugins == nil {
		return plugin.StatusResult{}, errNoPlugins
	}
	return a.plugins.Status(a.context(), id)
}

// RunPluginAction 运行插件声明的一个操作按钮，返回输出。
func (a *App) RunPluginAction(id, action string) (plugin.ActionResult, error) {
	if a.plugins == nil {
		return plugin.ActionResult{}, errNoPlugins
	}
	return a.plugins.RunAction(a.context(), id, action)
}

// ListCommands 返回已启用插件提供的斜杠命令（输入框的 / 菜单用）。
func (a *App) ListCommands() []plugin.CommandInfo {
	if a.plugins == nil {
		return []plugin.CommandInfo{}
	}
	return a.plugins.Commands()
}

// ExpandedCommand 是 ExpandCommand 的结果。
type ExpandedCommand struct {
	// Found 为 false 表示没有这个命令：界面照常把原文发出去。
	Found bool   `json:"found"`
	Text  string `json:"text"`
}

// ExpandCommand 把 "/name args" 展开成要发给模型的文本。
func (a *App) ExpandCommand(name, args string) (ExpandedCommand, error) {
	if a.plugins == nil {
		return ExpandedCommand{}, nil
	}
	text, ok, err := a.plugins.ExpandCommand(name, args)
	return ExpandedCommand{Found: ok, Text: text}, err
}

// ChooseFile 弹出系统的选择文件对话框（安装插件时选 .zip）。取消时返回空串。
func (a *App) ChooseFile(title, patternName, pattern string) (string, error) {
	start, _ := os.UserHomeDir()
	return runtime.OpenFileDialog(a.context(), runtime.OpenDialogOptions{
		Title:            title,
		DefaultDirectory: start,
		Filters:          []runtime.FileFilter{{DisplayName: patternName, Pattern: pattern}},
	})
}
