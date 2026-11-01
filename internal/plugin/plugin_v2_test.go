package plugin

import (
    "context"
    "encoding/json"
    "path/filepath"
    "strings"
    "testing"

    "github.com/crazyjums/myllmapp/internal/mcp"
)

func TestPortablePluginImportAndSafety(t *testing.T) {
    dir := t.TempDir()
    writeFile(t, filepath.Join(dir, "plugin.json"), `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"portable","version":"1.0.0","description":"Portable"}`)
    writeFile(t, filepath.Join(dir, "skills", "review", "SKILL.md"), "---\nname: review\ndescription: Review code\n---\nReview this code")
    writeFile(t, filepath.Join(dir, "mcp.json"), `{"mcpServers":{"tools":{"type":"stdio","command":"python3","args":["server.py"]}}}`)
    m, err := ParseManifest(dir, "dev")
    if err != nil { t.Fatalf("agent package: %v", err) }
    if m.ID != "portable" || len(m.Skills) != 1 || m.Skills[0].Path != "skills/review" || len(m.MCP) != 1 || m.MCP[0].Command != "python3" {
        t.Fatalf("agent package should map into Mosaic Manifest: %+v", m)
    }

    h, _, _, _ := newTestHost(t)
    preview, err := h.Preview(dir)
    if err != nil { t.Fatalf("preview portable: %v", err) }
    if preview.Plugin.ID != "portable" || len(preview.Plugin.Skills) != 1 || len(preview.Plugin.Servers) != 1 {
        t.Fatalf("preview differs from imported manifest: %+v", preview.Plugin)
    }
    if preview.Plugin.Servers[0].Effective != "exec" { t.Fatalf("untrusted MCP must be exec by default") }

    // Unsupported executable components must fail before install.
    writeFile(t, filepath.Join(dir, "hooks", "hooks.json"), `{"hooks":[]}`)
    if _, err := ParseManifest(dir, "dev"); err == nil || !strings.Contains(err.Error(), "hooks") {
        t.Fatalf("hooks must not be silently ignored: %v", err)
    }
}

func TestCodexLegacyPluginRoot(t *testing.T) {
    dir := t.TempDir()
    writeFile(t, filepath.Join(dir, ".codex-plugin", "plugin.json"), `{"name":"legacy-codex","version":"1.2.3","description":"Legacy Codex package"}`)
    writeFile(t, filepath.Join(dir, "skills", "helper", "SKILL.md"), "---\nname: helper\ndescription: Helper\n---\nHelper")
    if root, err := pluginRoot(dir); err != nil || root != dir {
        t.Fatalf("legacy root was not found: %s %v", root, err)
    }
    m, err := ParseManifest(dir, "dev")
    if err != nil || m.ID != "legacy-codex" || len(m.Skills) != 1 {
        t.Fatalf("legacy package: %+v %v", m, err)
    }
}

func TestPluginDiagnosticsAndDisabledActions(t *testing.T) {
    h, _, _, _ := newTestHost(t)
    dir := t.TempDir()
    m := baseManifest("demo")
    m["status"] = map[string]any{"command": helperCmd("plain-ok")}
    m["actions"] = []any{map[string]any{"id":"selftest","label":"Selftest","command":helperCmd("plain-ok")}}
    writePlugin(t, dir, m)
    installDir(t, h, dir)

    diag, err := h.Diagnostics("demo")
    if err != nil || diag.State != HealthNeedsSetup || diag.Code != "CONFIG_REQUIRED" {
        t.Fatalf("required secrets must show needs_setup: %+v %v", diag, err)
    }
    if _, err := h.SetConfig("demo", map[string]string{"token":"secret","profile":"prod"}); err != nil { t.Fatal(err) }
    diag, err = h.Diagnostics("demo")
    if err != nil || diag.State != HealthStarting || diag.Code != "MCP_STATUS_UNAVAILABLE" {
        t.Fatalf("fake MCP has no live statuses: %+v %v", diag, err)
    }
    if _, err := h.SetEnabled("demo", false); err != nil { t.Fatal(err) }
    diag, err = h.Diagnostics("demo")
    if err != nil || diag.State != HealthDisabled || diag.Code != "PLUGIN_DISABLED" {
        t.Fatalf("disabled plugins cannot be ready: %+v %v", diag, err)
    }
    out, err := h.RunAction(context.Background(), "demo", "selftest")
    if err != nil || !strings.Contains(out.Error, "已停用") {
        t.Fatalf("disabled action should be blocked: %+v %v", out, err)
    }
    if _, err := h.StartLogin("demo"); err == nil {
        // The manifest does not declare login. Either way, a disabled plugin
        // cannot start any executable action.
        t.Fatal("disabled plugin must not launch login")
    }
}

type fakeHealthMCP struct {
    *fakeMCP
    statuses []mcp.ServerStatus
    reconnected []string
}

func (m *fakeHealthMCP) Statuses() []mcp.ServerStatus { return m.statuses }
func (m *fakeHealthMCP) Reconnect(name string) error {
    m.reconnected = append(m.reconnected, name)
    return nil
}

func TestPluginDiagnosticsUsesActualMCPToolRegistration(t *testing.T) {
    tmp := t.TempDir()
    fm := &fakeHealthMCP{fakeMCP: newFakeMCP()}
    h, err := NewHost(HostOptions{Dir: filepath.Join(tmp, "plugins"), DataDir: filepath.Join(tmp, "data"),
        StateFile: filepath.Join(tmp, "plugins.json"), MCP: fm, AppVersion: "dev",
        LookPath: func(string) (string, error) { return "python3", nil }})
    if err != nil { t.Fatal(err) }
    dir := t.TempDir()
    m := baseManifest("demo")
    delete(m, "config")
    // Make the MCP configuration independent of missing secrets.
    m["mcp"] = []any{map[string]any{"command": "python3", "args": []string{"server.py"}}}
    // A source file is not launched by the fake transport, only registered.
    writePlugin(t, dir, m)
    writeFile(t, filepath.Join(dir, "server.py"), "print('hello')")
    installDir(t, h, dir)
    fm.statuses = []mcp.ServerStatus{{Name:"demo", Source:"plugin:demo", State:mcp.StateConnected, Tools:[]string{"mcp__demo__echo"}}}
    diag, err := h.Diagnostics("demo")
    if err != nil || diag.State != HealthReady || diag.ToolCount != 1 {
        t.Fatalf("real MCP tools must produce ready: %+v %v", diag, err)
    }
    if _, err := h.ReconnectPlugin("demo"); err != nil || len(fm.reconnected) != 1 || fm.reconnected[0] != "demo" {
        t.Fatalf("reconnect must target plugin's own service: %+v %v", fm.reconnected, err)
    }
    fm.statuses[0].State = mcp.StateFailed
    fm.statuses[0].Tools = nil
    fm.statuses[0].Error = "handshake timeout"
    diag, err = h.Diagnostics("demo")
    if err != nil || diag.State != HealthFailed || diag.ToolCount != 0 {
        t.Fatalf("failed MCP must not be marked enabled/ready: %+v %v", diag, err)
    }
}

func TestStandardPluginRejectsInvalidMCPTransport(t *testing.T) {
    dir := t.TempDir()
    body, _ := json.Marshal(map[string]any{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name":"bad-transport","version":"1.0.0"})
    writeFile(t, filepath.Join(dir, "plugin.json"), string(body))
    writeFile(t, filepath.Join(dir, "mcp.json"), `{"mcpServers":{"server":{"type":"sse","url":"https://example.com/sse"}}}`)
    if _, err := ParseManifest(dir, "dev"); err == nil || !strings.Contains(err.Error(), "不支持") {
        t.Fatalf("unsupported MCP transport must be rejected: %v", err)
    }
}
