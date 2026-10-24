package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/plugin"
)

// 本文件是"设置 → 权限与工具 / MCP / 外观 / 状态栏脚本"的后端部分。
// 每个方法的模式都一样：校验 → 应用到运行中的组件 → 写盘（persist）→ 更新内存。

// ---- 权限规则 ----

// maxRules 是规则条数上限：规则按顺序逐条匹配，几百条已经远超手工维护的范围。
const maxRules = 200

// PermissionRules 返回保存的权限规则。
func (m *Manager) PermissionRules() []permission.Rule {
	m.mu.Lock()
	defer m.mu.Unlock()
	return nonNilRules(m.file.PermissionRules)
}

// SetPermissionRules 保存权限规则，立即对之后的工具调用生效。
func (m *Manager) SetPermissionRules(rules []permission.Rule) error {
	if len(rules) > maxRules {
		return fmt.Errorf("规则太多（上限 %d 条）", maxRules)
	}
	for i := range rules {
		rules[i].Tool = strings.TrimSpace(rules[i].Tool)
		rules[i].Pattern = strings.TrimSpace(rules[i].Pattern)
		if err := rules[i].Validate(); err != nil {
			return fmt.Errorf("第 %d 条规则：%w", i+1, err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		if err := m.engine.SetPermissionRules(rules); err != nil {
			return err
		}
	}
	next := cloneFile(m.file)
	next.PermissionRules = slices.Clone(rules)
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

func nonNilRules(r []permission.Rule) []permission.Rule {
	if r == nil {
		return []permission.Rule{}
	}
	return slices.Clone(r)
}

// ---- 工具开关 ----

// DisabledTools 返回保存的被禁用工具名。
func (m *Manager) DisabledTools() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return nonNil(slices.Clone(m.file.DisabledTools))
}

// SetDisabledTools 保存被禁用的工具，从下一步模型调用开始生效。
func (m *Manager) SetDisabledTools(names []string) error {
	var clean []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(clean, n) {
			clean = append(clean, n)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneFile(m.file)
	next.DisabledTools = clean
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	if m.engine != nil {
		m.engine.SetDisabledTools(clean)
	}
	return nil
}

// ---- 状态栏脚本 ----

const maxScriptLen = 4096

// StatusLineScript 返回保存的状态栏命令。
func (m *Manager) StatusLineScript() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.file.StatusLineScript
}

// SetStatusLineScript 保存状态栏命令；空字符串表示不使用。
func (m *Manager) SetStatusLineScript(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if len(cmd) > maxScriptLen {
		return fmt.Errorf("命令过长（上限 %d 字节）", maxScriptLen)
	}
	if strings.ContainsAny(cmd, "\n\r") {
		return errors.New("命令只能有一行；复杂的逻辑请写成脚本文件再在这里调用")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneFile(m.file)
	next.StatusLineScript = cmd
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

// RunStatusLine 运行保存的状态栏命令（工作目录是当前工作区），返回输出的第一行。
// 没有配置命令时返回空字符串。
//
// 命令只从设置文件读取，不接受调用方传入：前端（WebView）不应该能让后端执行任意命令。
func (m *Manager) RunStatusLine(ctx context.Context, extra map[string]any) (string, error) {
	m.mu.Lock()
	cmd, eng := m.file.StatusLineScript, m.engine
	m.mu.Unlock()
	if cmd == "" || eng == nil {
		return "", nil
	}
	st := eng.Status()
	return plugin.CommandStatusLine{Command: cmd, Dir: st.Workspace}.Render(ctx, plugin.StatusInfo{
		SessionID:    st.SessionID,
		Provider:     st.Provider,
		Model:        st.Model,
		InputTokens:  st.Usage.InputTokens,
		OutputTokens: st.Usage.OutputTokens,
		Workspace:    st.Workspace,
		Extra:        extra,
	})
}

// TestStatusLineScript 用给定的命令试运行一次（设置页的"试运行"按钮），不保存。
//
// 这里接受传入的命令：用户正在设置页里编辑它，试运行等同于保存后运行；
// 它和 RunStatusLine 的区别只在于后者会被定时调用。
func (m *Manager) TestStatusLineScript(ctx context.Context, cmd string) (string, error) {
	m.mu.Lock()
	eng := m.engine
	m.mu.Unlock()
	info := plugin.StatusInfo{}
	if eng != nil {
		st := eng.Status()
		info = plugin.StatusInfo{SessionID: st.SessionID, Provider: st.Provider, Model: st.Model, Workspace: st.Workspace,
			InputTokens: st.Usage.InputTokens, OutputTokens: st.Usage.OutputTokens}
	}
	return plugin.CommandStatusLine{Command: strings.TrimSpace(cmd), Dir: info.Workspace}.Render(ctx, info)
}

// ---- MCP ----

const maxMCPServers = 50

// MCPServers 返回保存的 MCP server 配置。
func (m *Manager) MCPServers() []mcp.ServerConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file.MCPServers == nil {
		return []mcp.ServerConfig{}
	}
	return slices.Clone(m.file.MCPServers)
}

// SaveMCPServers 保存整个 MCP server 列表，并让连接与之同步（新增的连接、删除的断开、修改的重连）。
func (m *Manager) SaveMCPServers(list []mcp.ServerConfig) error {
	if len(list) > maxMCPServers {
		return fmt.Errorf("MCP server 太多（上限 %d 个）", maxMCPServers)
	}
	seen := map[string]bool{}
	for i := range list {
		c := &list[i]
		c.Name, c.Command, c.URL = strings.TrimSpace(c.Name), strings.TrimSpace(c.Command), strings.TrimSpace(c.URL)
		if err := c.Validate(); err != nil {
			return err
		}
		if seen[c.Name] {
			return fmt.Errorf("MCP 名称 %q 重复", c.Name)
		}
		seen[c.Name] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneFile(m.file)
	next.MCPServers = slices.Clone(list)
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	if m.rtBase.MCP != nil {
		m.rtBase.MCP.Apply(list)
	}
	return nil
}

// MCPStatuses 返回各 server 的连接状态。
func (m *Manager) MCPStatuses() []mcp.ServerStatus {
	m.mu.Lock()
	mgr := m.rtBase.MCP
	m.mu.Unlock()
	if mgr == nil {
		return []mcp.ServerStatus{}
	}
	return mgr.Statuses()
}

// ReconnectMCP 重新连接一个 server。
func (m *Manager) ReconnectMCP(name string) error {
	m.mu.Lock()
	mgr := m.rtBase.MCP
	m.mu.Unlock()
	if mgr == nil {
		return errors.New("MCP 未启用")
	}
	return mgr.Reconnect(name)
}

// ---- 界面偏好 ----

// UIPrefs 返回保存的界面偏好（JSON 对象）；从未保存过时返回 null。
func (m *Manager) UIPrefs() json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return orNull(m.file.UI)
}

// SaveUIPrefs 保存界面偏好。与状态栏配置一样，字段由前端定义。
func (m *Manager) SaveUIPrefs(raw json.RawMessage) error {
	return m.saveOpaque(raw, "ui", func(f *File, v json.RawMessage) { f.UI = v })
}

// ---- 工具列表 ----

// Tools 返回所有工具及开关状态（含已连接的 MCP 工具）。
func (m *Manager) Tools() []core.ToolInfo {
	m.mu.Lock()
	eng := m.engine
	m.mu.Unlock()
	if eng == nil {
		return []core.ToolInfo{}
	}
	return eng.Tools()
}
