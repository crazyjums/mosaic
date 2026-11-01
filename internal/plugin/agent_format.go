package plugin

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"

    "github.com/crazyjums/myllmapp/internal/mcp"
    "github.com/crazyjums/myllmapp/internal/permission"
)

// importAgentComponents maps the portable Agent Plugins / Codex layout into
// Mosaic's existing validated Manifest. Unsupported executable components
// (hooks / subagents) are rejected instead of silently granted privileges.
func importAgentComponents(dir string, m *Manifest) error {
    for _, rel := range []string{"hooks", "agents"} {
        p := filepath.Join(dir, rel)
        if _, err := os.Stat(p); err == nil {
            return fmt.Errorf("插件包含 %s：Mosaic 尚未支持该扩展，请移除后再安装", rel)
        } else if !os.IsNotExist(err) {
            return fmt.Errorf("检查扩展 %s 失败：%w", rel, err)
        }
    }
    if m.ID == "" { m.ID = m.Name }
    if m.Name == "" { m.Name = m.ID }
    // Skills are conventionally discovered from skills/<id>/SKILL.md.
    if len(m.Skills) == 0 {
        root := filepath.Join(dir, "skills")
        entries, err := os.ReadDir(root)
        if err != nil && !os.IsNotExist(err) { return fmt.Errorf("扫描 skills：%w", err) }
        for _, entry := range entries {
            if !entry.IsDir() { continue }
            rel := filepath.ToSlash(filepath.Join("skills", entry.Name()))
            if _, err := os.Stat(filepath.Join(root, entry.Name(), "SKILL.md")); err == nil {
                m.Skills = append(m.Skills, SkillRef{Path: rel})
            }
        }
        sort.Slice(m.Skills, func(i, j int) bool { return m.Skills[i].Path < m.Skills[j].Path })
    }
    // A standard mcp.json is data, not executable JavaScript. We support
    // stdio and streamable-http entries only; validation is reused below.
    p := filepath.Join(dir, "mcp.json")
    raw, err := os.ReadFile(p)
    if os.IsNotExist(err) {
        // Older Codex plugin packages commonly use .mcp.json.
        p = filepath.Join(dir, ".mcp.json")
        raw, err = os.ReadFile(p)
    }
    if os.IsNotExist(err) { return nil }
    if err != nil { return err }
    if len(raw) > maxManifestBytes { return fmt.Errorf("mcp.json 超过大小限制") }
    var config struct {
        MCPServers map[string]struct {
            Type string `json:"type"`
            Command string `json:"command"`
            Args []string `json:"args"`
            Env map[string]string `json:"env"`
            URL string `json:"url"`
            Headers map[string]string `json:"headers"`
        } `json:"mcpServers"`
    }
    if err := json.Unmarshal(raw, &config); err != nil {
        return fmt.Errorf("mcp.json 不是合法 JSON：%w", err)
    }
    if len(config.MCPServers) == 0 { return fmt.Errorf("mcp.json 没有 mcpServers 定义") }
    if len(m.MCP) > 0 { return fmt.Errorf("插件同时在 plugin.json 和 mcp.json 声明 MCP，请保留一份") }
    names := make([]string, 0, len(config.MCPServers))
    for name := range config.MCPServers { names = append(names, name) }
    sort.Strings(names)
    for _, name := range names {
        c := config.MCPServers[name]
        spec := MCPSpec{Name: name, Command: c.Command, Args: c.Args, Env: c.Env, URL: c.URL, Headers: c.Headers, Risk: permission.RiskExec}
        switch strings.ToLower(c.Type) {
        case "", "stdio":
            spec.Transport = mcp.TransportStdio
        case "http", "streamable-http":
            spec.Transport = mcp.TransportHTTP
        default:
            return fmt.Errorf("mcp 服务 %q 使用了不支持的传输类型 %q", name, c.Type)
        }
        m.MCP = append(m.MCP, spec)
        // Portable MCP declarations do not have Mosaic's requires block.
        // Detect plain executable names up front, rather than failing only
        // after an async MCP handshake timeout. Paths are not LookPath targets.
        if spec.Transport == mcp.TransportStdio && reqNameRe.MatchString(spec.Command) && !contains(m.Requires.Commands, spec.Command) {
            m.Requires.Commands = append(m.Requires.Commands, spec.Command)
        }
    }
    return nil
}
