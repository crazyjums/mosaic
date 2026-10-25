package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// FileName 是设置文件名，位于数据目录下。
const FileName = "settings.json"

// fileVersion 是设置文件格式版本。字段只增不改；需要不兼容修改时 +1 并在 Load 中迁移。
const fileVersion = 1

// ProviderSetting 是界面上配置的一个厂商配置档。字段与 provider.ProfileConfig 对应。
type ProviderSetting struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	// APIKeyRef 为 "keychain" 表示密钥保存在系统钥匙串里，文件中的 APIKey 为空（见 keychain.go）。
	APIKeyRef    string         `json:"api_key_ref,omitempty"`
	Models       []string       `json:"models"`
	DefaultModel string         `json:"default_model,omitempty"`
	Options      map[string]any `json:"options,omitempty"`
}

// File 是设置文件的内容。
type File struct {
	Version int `json:"version"`
	// DefaultProvider / DefaultModel 记住上次在界面上选择的厂商和模型，下次启动直接使用。
	DefaultProvider string            `json:"default_provider,omitempty"`
	DefaultModel    string            `json:"default_model,omitempty"`
	Providers       []ProviderSetting `json:"providers"`
	// Groups 是模型组（故障转移）。它们和厂商配置档一起出现在模型下拉框里，见 groups.go。
	Groups []provider.GroupConfig `json:"groups,omitempty"`
	// Proxy 是全局默认代理，没有单独设置代理的厂商都用它。地址里可能带密码，和 API Key 一样存在本文件里（权限 600）。
	Proxy string `json:"proxy,omitempty"`

	// General 是"设置 → 常规 / 系统提示词"中的参数，零值字段表示沿用配置文件。
	General General `json:"general"`

	// PermissionMode 记住在状态栏上切换的权限模式；为空表示使用 config.toml 中的设置。
	PermissionMode string `json:"permission_mode,omitempty"`

	// StatusLine 是前端状态栏的配置（显示哪些项、顺序、显示方式）。
	//
	// 后端把它当作不透明的 JSON 原样保存，不解析其中的字段：状态栏长什么样是纯界面的事，
	// 新增一个状态栏项只需要改前端，不需要动 Go 代码和文件格式。
	// 考虑过存在浏览器的 localStorage 里，但那样 CLI 和备份工具都看不到，换机器迁移也不方便。
	StatusLine json.RawMessage `json:"statusline,omitempty"`

	// ---- 设置 → 权限与工具 ----

	// PermissionRules 是细粒度的权限规则（按工具名和命令/路径匹配），优先于权限档位。
	PermissionRules []permission.Rule `json:"permission_rules,omitempty"`
	// DisabledTools 是被关掉的工具名（含 MCP 工具，名字带 mcp__ 前缀）。
	DisabledTools []string `json:"disabled_tools,omitempty"`

	// StatusLineScript 是自定义状态栏命令，输出显示在状态栏的"脚本"项中。
	// 它和 StatusLine 分开存：StatusLine 是前端随意改写的不透明 JSON，而命令会被执行，
	// 只能通过专门的方法修改，运行时也只从这里读取，不接受前端传入的命令。
	StatusLineScript string `json:"statusline_script,omitempty"`

	// MCPServers 是 MCP server 的连接配置（设置 → MCP）。
	MCPServers []mcp.ServerConfig `json:"mcp_servers,omitempty"`

	// CodexQuota 为 true 时，读取本机 Codex CLI 的登录信息查询 ChatGPT 套餐的剩余额度（见 internal/codexquota）。
	// 用的是未公开的接口，所以默认关闭，由用户在 ChatGPT 账号设置里开启。
	CodexQuota bool `json:"codex_quota,omitempty"`

	// UseKeychain 为 true 时，厂商的 API Key 保存在系统钥匙串里，而不是本文件中。
	UseKeychain bool `json:"use_keychain,omitempty"`

	// UI 是外观、交互、语言等纯界面偏好，与 StatusLine 一样由后端原样保存（设置 → 外观）。
	UI json.RawMessage `json:"ui,omitempty"`
}

// Load 读取设置文件。文件不存在时返回空设置（第一次启动的正常情况）。
func Load(path string) (File, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{Version: fileVersion}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("settings: read %s: %w", path, err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("settings: parse %s: %w", path, err)
	}
	if f.Version == 0 {
		f.Version = fileVersion
	}
	return f, nil
}

// Save 原子地写入设置文件：先写临时文件再重命名，写到一半崩溃也不会留下损坏的文件。
// 文件包含 API Key，权限设为 0600（仅当前用户可读写）。
func Save(path string, f File) error {
	f.Version = fileVersion
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("settings: create dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, FileName+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // 重命名成功后这里删除的是不存在的文件，无害
	if err := tmp.Chmod(0o600); err != nil && !isChmodUnsupported(err) {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// isChmodUnsupported：Windows 上 Chmod 只能改只读位，权限由 ACL 控制；此时忽略即可。
func isChmodUnsupported(err error) bool {
	return errors.Is(err, errors.ErrUnsupported)
}
