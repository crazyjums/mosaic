package plugin

import (
    "fmt"
    "strings"
    "time"

    "github.com/crazyjums/myllmapp/internal/mcp"
)

// HealthState describes a plugin's actual capabilities, not merely its enabled flag.
type HealthState string

const (
    HealthDisabled   HealthState = "disabled"
    HealthNeedsSetup HealthState = "needs_setup"
    HealthStarting   HealthState = "starting"
    HealthReady      HealthState = "ready"
    HealthDegraded   HealthState = "degraded"
    HealthFailed     HealthState = "failed"
)

type ServerHealth struct {
    Name    string    `json:"name"`
    State   mcp.State `json:"state"`
    Tools   []string  `json:"tools"`
    Error   string    `json:"error,omitempty"`
    // StartedAt / Output 来自 MCP 连接：连接中据此显示“已等待 N 秒”和子进程最近的输出，失败后保留输出供排查。
    StartedAt time.Time `json:"started_at,omitempty"`
    Output    string    `json:"output,omitempty"`
}

type PluginDiagnostics struct {
    ID           string         `json:"id"`
    Enabled      bool           `json:"enabled"`
    State        HealthState    `json:"state"`
    Message      string         `json:"message,omitempty"`
    Code         string         `json:"code,omitempty"`
    Skills       []string       `json:"skills"`
    Commands     []string       `json:"commands"`
    Servers      []ServerHealth `json:"servers"`
    ToolCount    int            `json:"tool_count"`
    Missing      []string       `json:"missing"`
    CheckedAt    time.Time      `json:"checked_at"`
}

// Diagnostics combines the installed plugin declaration and actual MCP runtime
// state. It never launches a plugin command, reads a secret, or makes a tool call.
func (h *Host) Diagnostics(id string) (PluginDiagnostics, error) {
    info, err := h.Get(id)
    if err != nil {
        return PluginDiagnostics{}, err
    }
    result := PluginDiagnostics{
        ID: id, Enabled: info.Enabled, State: HealthReady,
        Skills: append([]string{}, info.Skills...),
        Commands: []string{}, Servers: []ServerHealth{}, Missing: []string{},
        CheckedAt: time.Now(),
    }
    for _, c := range info.Commands { result.Commands = append(result.Commands, c.Name) }
    for _, dep := range info.Requires {
        if !dep.Found { result.Missing = append(result.Missing, dep.Command) }
    }
    switch {
    case !info.Enabled:
        result.State, result.Code, result.Message = HealthDisabled, "PLUGIN_DISABLED", "插件已停用"
        return result, nil
    case info.Error != "":
        result.State, result.Code, result.Message = HealthFailed, "PLUGIN_INVALID", info.Error
        return result, nil
    case len(result.Missing) != 0:
        result.State, result.Code = HealthNeedsSetup, "DEPENDENCY_NOT_FOUND"
        result.Message = "缺少外部命令：" + strings.Join(result.Missing, "、")
        return result, nil
    case info.Warning != "":
        result.State, result.Code, result.Message = HealthNeedsSetup, "CONFIG_REQUIRED", info.Warning
        return result, nil
    }
    if len(info.Servers) == 0 {
        if len(info.Skills) == 0 && len(info.Commands) == 0 {
            result.State, result.Code, result.Message = HealthFailed, "PLUGIN_HAS_NO_CAPABILITIES", "插件没有可使用的技能、命令或 MCP 工具"
        } else {
            result.Message = "技能和斜杠命令已就绪（不需要 MCP 连接）"
        }
        return result, nil
    }
    // The HostOptions.MCP interface stays backward compatible with existing
    // fake MCP implementations. Production mcp.Manager implements Statuses.
    source, ok := h.opts.MCP.(interface { Statuses() []mcp.ServerStatus })
    if !ok {
        result.State, result.Code = HealthStarting, "MCP_STATUS_UNAVAILABLE"
        result.Message = "当前 MCP 管理器不提供运行状态"
        return result, nil
    }
    byName := map[string]mcp.ServerStatus{}
    for _, st := range source.Statuses() {
        if st.Source == "plugin:"+id { byName[st.Name] = st }
    }
    var connected, starting, failed int
    for _, server := range info.Servers {
        status, found := byName[server.Name]
        health := ServerHealth{Name: server.Name, State: mcp.StateConnecting, Tools: []string{}}
        if found {
            health.State = status.State
            health.Tools = append(health.Tools, status.Tools...)
            health.Error = status.Error
            health.StartedAt = status.StartedAt
            health.Output = status.Output
        }
        switch {
        case found && status.State == mcp.StateConnected && len(status.Tools) > 0:
            connected++
            result.ToolCount += len(status.Tools)
        case !found || status.State == mcp.StateConnecting:
            starting++
        default:
            failed++
            if health.Error == "" && health.State == mcp.StateConnected { health.Error = "连接已建立，但没有注册任何工具" }
        }
        result.Servers = append(result.Servers, health)
    }
    switch {
    case failed == 0 && starting == 0:
        result.State, result.Message = HealthReady, fmt.Sprintf("%d 个 MCP 工具已就绪", result.ToolCount)
    case connected > 0:
        result.State, result.Code = HealthDegraded, "MCP_PARTIALLY_AVAILABLE"
        result.Message = fmt.Sprintf("%d/%d 个 MCP 服务可用，其他服务尚未就绪", connected, len(info.Servers))
    case starting > 0 && failed == 0:
        result.State, result.Code, result.Message = HealthStarting, "MCP_CONNECTING", "正在启动 MCP 服务并发现工具"
    default:
        result.State, result.Code = HealthFailed, "MCP_CONNECTION_FAILED"
        result.Message = "MCP 服务没有注册可用工具，请展开连接详情检查错误"
    }
    return result, nil
}

// ReconnectPlugin restarts only MCP services owned by this enabled plugin.
func (h *Host) ReconnectPlugin(id string) (PluginDiagnostics, error) {
    info, err := h.Get(id)
    if err != nil { return PluginDiagnostics{}, err }
    if !info.Enabled || info.Error != "" {
        return PluginDiagnostics{}, fmt.Errorf("插件 %q 未启用或无法加载", id)
    }
    if len(info.Servers) == 0 { return PluginDiagnostics{}, fmt.Errorf("插件 %q 没有需要重连的 MCP 服务", id) }
    if len(h.missingForInfo(info)) != 0 || info.Warning != "" {
        return PluginDiagnostics{}, fmt.Errorf("请先修复插件依赖与配置，再重连")
    }
    connector, ok := h.opts.MCP.(interface { Reconnect(string) error })
    if !ok { return PluginDiagnostics{}, fmt.Errorf("当前 MCP 管理器不支持重连") }
    for _, srv := range info.Servers {
        if err := connector.Reconnect(srv.Name); err != nil {
            return PluginDiagnostics{}, fmt.Errorf("重连 %s 失败：%w", srv.Name, err)
        }
    }
    return h.Diagnostics(id)
}

func (h *Host) missingForInfo(info Info) []string {
    var missing []string
    for _, dep := range info.Requires {
        if !dep.Found { missing = append(missing, dep.Command) }
    }
    return missing
}
