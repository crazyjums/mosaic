package app

import (
    "context"
    "encoding/json"
    "os/exec"
    "strings"
    "time"
)

// ClaudeCodeStatus 仅检测用户安装的官方 Claude Code CLI 的登录状态。
// 不读取、不保存 OAuth 凭证，也不把订阅凭证转交给 Mosaic 的模型 Provider。
type ClaudeCodeStatus struct {
    Installed bool `json:"installed"`
    LoggedIn bool `json:"logged_in"`
    AuthMethod string `json:"auth_method,omitempty"`
    Message string `json:"message"`
}

func (a *App) GetClaudeCodeStatus() ClaudeCodeStatus {
    path, err := exec.LookPath("claude")
    if err != nil {
        return ClaudeCodeStatus{Message: "未检测到 Claude Code CLI，请先安装官方 Claude Code 并确保 claude 命令位于 PATH"}
    }
    ctx, cancel := context.WithTimeout(a.context(), 5*time.Second)
    defer cancel()
    out, err := exec.CommandContext(ctx, path, "auth", "status").Output()
    if ctx.Err() != nil {
        return ClaudeCodeStatus{Installed: true, Message: "Claude Code 登录状态检查超时"}
    }
    if err != nil {
        return ClaudeCodeStatus{Installed: true, Message: "Claude Code 尚未登录，或当前 CLI 不支持 auth status"}
    }
    var status struct {
        LoggedIn bool `json:"loggedIn"`
        AuthMethod string `json:"authMethod"`
    }
    if json.Unmarshal(out, &status) == nil {
        if !status.LoggedIn {
            return ClaudeCodeStatus{Installed: true, Message: "Claude Code 尚未登录"}
        }
        return ClaudeCodeStatus{Installed: true, LoggedIn: true, AuthMethod: status.AuthMethod, Message: "官方 Claude Code CLI 已登录"}
    }
    // 旧版 CLI 的 auth status 可能返回普通文本，不解析或暴露原文里的账号信息。
    if strings.Contains(strings.ToLower(string(out)), "not logged") {
        return ClaudeCodeStatus{Installed: true, Message: "Claude Code 尚未登录"}
    }
    return ClaudeCodeStatus{Installed: true, LoggedIn: true, Message: "官方 Claude Code CLI 已登录（请在 CLI 中运行 /status 确认计费来源）"}
}
