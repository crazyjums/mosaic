package app

import (
    "context"
    "encoding/json"
    "os/exec"
    "fmt"
    "runtime"
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

// LaunchClaudeCodeLogin 在用户本机终端启动原版 Claude Code CLI，由其自行完成订阅登录。
// 不修改 CLI、不抓取令牌、不将订阅认证当成第三方 Provider 的 API 凭证。
func (a *App) LaunchClaudeCodeLogin() error {
    if _, err := exec.LookPath("claude"); err != nil {
        return fmt.Errorf("没有找到 Claude Code CLI，请先安装官方 Claude Code 并确保 claude 在 PATH 中")
    }
    var cmd *exec.Cmd
    switch runtime.GOOS {
    case "windows":
        // 全部参数固定，不接受来自模型或网页的命令文本。
        cmd = exec.Command("cmd.exe", "/c", "start", "\"\"", "cmd.exe", "/k", "claude")
    case "darwin":
        cmd = exec.Command("osascript", "-e", "tell application \"Terminal\" to do script \"claude\"")
    default:
        if _, err := exec.LookPath("x-terminal-emulator"); err == nil {
            cmd = exec.Command("x-terminal-emulator", "-e", "claude")
        } else if _, err := exec.LookPath("gnome-terminal"); err == nil {
            cmd = exec.Command("gnome-terminal", "--", "claude")
        } else if _, err := exec.LookPath("xterm"); err == nil {
            cmd = exec.Command("xterm", "-e", "claude")
        } else {
            return fmt.Errorf("未找到终端程序，请手动打开终端执行 claude，再输入 /login")
        }
    }
    if err := cmd.Start(); err != nil { return fmt.Errorf("无法启动 Claude Code 终端：%w", err) }
    go cmd.Wait()
    return nil
}
