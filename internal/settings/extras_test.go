package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/secret"
	"github.com/crazyjums/myllmapp/internal/tool"
	"github.com/crazyjums/myllmapp/internal/tool/builtin"
)

func TestPermissionRulesAndDisabledTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	m, _ := NewManager(path, []provider.ProfileConfig{{Name: "fake", Type: "fake", Models: []string{"demo"}}}, provider.DefaultRegistry)
	tools := tool.NewRegistry()
	builtin.Register(tools, builtin.Options{})
	policy := permission.NewRulePolicy(permission.NewModePolicy(permission.ModeDefault), nil)
	eng, err := core.NewEngine(core.EngineConfig{Catalog: m.Catalog(), Tools: tools, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	m.Attach(eng)

	if err := m.SetPermissionRules([]permission.Rule{{Tool: " shell ", Pattern: "go test*", Action: permission.Allow}}); err != nil {
		t.Fatal(err)
	}
	if len(policy.Rules()) != 1 || policy.Rules()[0].Tool != "shell" {
		t.Fatalf("规则应生效并去掉空白: %+v", policy.Rules())
	}
	if err := m.SetPermissionRules([]permission.Rule{{Tool: "shell", Action: "maybe"}}); err == nil {
		t.Fatal("非法规则应报错")
	}
	if len(m.PermissionRules()) != 1 {
		t.Fatal("非法规则不应覆盖")
	}

	if err := m.SetDisabledTools([]string{"shell", "shell", " "}); err != nil {
		t.Fatal(err)
	}
	var disabled []string
	for _, ti := range m.Tools() {
		if ti.Disabled {
			disabled = append(disabled, ti.Name)
		}
	}
	if len(disabled) != 1 || disabled[0] != "shell" {
		t.Fatalf("工具开关应去重并生效: %v", disabled)
	}

	m2, _ := NewManager(path, nil, provider.DefaultRegistry)
	if len(m2.PermissionRules()) != 1 || len(m2.DisabledTools()) != 1 {
		t.Fatal("应持久化")
	}
}

func TestStatusLineScript(t *testing.T) {
	m, _, _, path := newGeneralEnv(t, "")
	if out, err := m.RunStatusLine(context.Background(), nil); out != "" || err != nil {
		t.Fatal("未配置时应返回空")
	}
	if err := m.SetStatusLineScript("echo a\necho b"); err == nil {
		t.Fatal("多行命令应拒绝")
	}
	if err := m.SetStatusLineScript("echo hi"); err != nil {
		t.Fatal(err)
	}
	out, err := m.RunStatusLine(context.Background(), map[string]any{"x": 1})
	if err != nil || strings.TrimSpace(out) != "hi" {
		t.Fatalf("run: %q %v", out, err)
	}
	if out, _ := m.TestStatusLineScript(context.Background(), "echo try"); strings.TrimSpace(out) != "try" {
		t.Fatalf("试运行: %q", out)
	}
	m2, _ := NewManager(path, nil, provider.DefaultRegistry)
	if m2.StatusLineScript() != "echo hi" {
		t.Fatal("应持久化")
	}
}

func TestSaveMCPServers(t *testing.T) {
	m, _, _, path := newGeneralEnv(t, "")
	reg := tool.NewRegistry()
	mgr := mcp.NewManager(reg)
	defer mgr.Close()
	m.mu.Lock()
	m.rtBase.MCP = mgr
	m.mu.Unlock()

	if err := m.SaveMCPServers([]mcp.ServerConfig{{Name: "bad name", Transport: "stdio", Command: "x"}}); err == nil {
		t.Fatal("非法名称应报错")
	}
	dup := []mcp.ServerConfig{{Name: "a", Transport: "stdio", Command: "x", Disabled: true}, {Name: "a", Transport: "stdio", Command: "y"}}
	if err := m.SaveMCPServers(dup); err == nil {
		t.Fatal("重名应报错")
	}
	if err := m.SaveMCPServers([]mcp.ServerConfig{{Name: "a", Transport: "stdio", Command: " npx ", Disabled: true}}); err != nil {
		t.Fatal(err)
	}
	if st := m.MCPStatuses(); len(st) != 1 || st[0].State != mcp.StateDisabled {
		t.Fatalf("应同步到连接管理器: %+v", st)
	}
	m2, _ := NewManager(path, nil, provider.DefaultRegistry)
	if s := m2.MCPServers(); len(s) != 1 || s[0].Command != "npx" {
		t.Fatalf("应持久化: %+v", s)
	}
}

func TestUIPrefs(t *testing.T) {
	m, _, path := newEnv(t)
	if string(m.UIPrefs()) != "null" {
		t.Fatal("默认应为 null")
	}
	if err := m.SaveUIPrefs(json.RawMessage(`[1]`)); err == nil {
		t.Fatal("必须是对象")
	}
	if err := m.SaveUIPrefs(json.RawMessage(`{"theme":"dark"}`)); err != nil {
		t.Fatal(err)
	}
	m2, _ := NewManager(path, nil, provider.DefaultRegistry)
	var got map[string]string
	if json.Unmarshal(m2.UIPrefs(), &got); got["theme"] != "dark" {
		t.Fatal(string(m2.UIPrefs()))
	}
}

func TestTitleModeValidation(t *testing.T) {
	m, eng, _, _ := newGeneralEnv(t, "")
	if err := m.SaveGeneral(General{TitleMode: "random"}); err == nil {
		t.Fatal("未知方式应报错")
	}
	if err := m.SaveGeneral(General{TitleMode: TitleModel}); err != nil {
		t.Fatal(err)
	}
	if eng.Runtime().TitleMode != TitleModel {
		t.Fatal("应下发到引擎")
	}
	if m.General().Effective.TitleMode != TitleModel || m.General().Defaults.TitleMode != TitleFirstMessage {
		t.Fatalf("视图: %+v", m.General())
	}
}

func TestKeychain(t *testing.T) {
	srv := fakeOpenAI(t, "sk-secret-123456")
	path := filepath.Join(t.TempDir(), FileName)
	store := secret.NewMemory()
	base := []provider.ProfileConfig{{Name: "fake", Type: "fake", Models: []string{"demo"}}}
	m, err := NewManagerWithSecrets(path, base, provider.DefaultRegistry, store)
	if err != nil {
		t.Fatal(err)
	}
	eng, _ := core.NewEngine(core.EngineConfig{Catalog: m.Catalog()})
	m.Attach(eng)
	in := ProviderInput{Name: "my", Type: "openai", BaseURL: srv.URL + "/v1", APIKey: "sk-secret-123456", Models: []string{"x"}}
	if err := m.Save(in); err != nil {
		t.Fatal(err)
	}
	fileHas := func(s string) bool { b, _ := os.ReadFile(path); return strings.Contains(string(b), s) }
	if !fileHas("sk-secret-123456") {
		t.Fatal("未开启钥匙串时密钥在文件中")
	}

	// 开启：密钥迁移到钥匙串，文件里只剩引用。
	if err := m.SetUseKeychain(true); err != nil {
		t.Fatal(err)
	}
	if fileHas("sk-secret-123456") || !fileHas(`"api_key_ref": "keychain"`) || store.Len() != 1 {
		t.Fatal("密钥应迁移到钥匙串")
	}
	// 重启后能读回密钥，厂商可用。
	m2, _ := NewManagerWithSecrets(path, base, provider.DefaultRegistry, store)
	if got := m2.existingKey("my"); got != "sk-secret-123456" {
		t.Fatalf("应从钥匙串读回: %q", got)
	}
	// 修改其他设置不会把密钥写回文件。
	if err := m2.SaveUIPrefs(json.RawMessage(`{}`)); err != nil || fileHas("sk-secret-123456") {
		t.Fatal("开启后任何写盘都不应写出明文")
	}

	// 钥匙串读取失败：引用保留，不丢失；给出警告。
	m3, _ := NewManagerWithSecrets(path, base, provider.DefaultRegistry, nil)
	if m3.Keychain().Warning == "" {
		t.Fatal("应提示读不出密钥")
	}
	m3.file.UseKeychain = false // 模拟在不支持的环境里修改设置
	if err := m3.SaveUIPrefs(json.RawMessage(`{"a":1}`)); err != nil || !fileHas(`"api_key_ref": "keychain"`) {
		t.Fatal("读不出的密钥引用应保留")
	}

	// 关闭：密钥写回文件，钥匙串条目删除。
	m4, _ := NewManagerWithSecrets(path, base, provider.DefaultRegistry, store)
	m4.file.UseKeychain = true
	if err := m4.SetUseKeychain(false); err != nil {
		t.Fatal(err)
	}
	if !fileHas("sk-secret-123456") || store.Len() != 0 {
		t.Fatal("关闭后应迁回文件并清理钥匙串")
	}

	// 钥匙串不可用时不能开启。
	store.Fail = os.ErrPermission
	if err := m4.SetUseKeychain(true); err == nil || m4.Keychain().Available {
		t.Fatal("不可用时应拒绝开启")
	}
}
