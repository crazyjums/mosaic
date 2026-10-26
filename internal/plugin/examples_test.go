package plugin

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/secret"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// 仓库里 catalog/bundled 下的示例必须一直是合法的、能装的插件：它们也是给插件作者看的文档。

func exampleDir(name string) string { return filepath.Join("..", "..", "catalog", "bundled", name) }

func newExampleHost(t *testing.T, mcpApplier MCPApplier) *Host {
	t.Helper()
	root := t.TempDir()
	h, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: secret.NewMemory(), MCP: mcpApplier, AppVersion: "1.3.2",
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestExampleHelloSkill(t *testing.T) {
	h := newExampleHost(t, newFakeMCP())
	info := installDir(t, h, exampleDir("hello-skill"))
	if info.Error != "" || !info.Enabled || len(info.Skills) != 1 || len(info.Commands) != 1 || len(info.Servers) != 0 {
		t.Fatalf("%+v", info)
	}
	text, ok, err := h.ExpandCommand("hello", "小明")
	if err != nil || !ok || !strings.Contains(text, "请用一句话问候：小明") {
		t.Fatalf("%q %v %v", text, ok, err)
	}
	if f := h.SkillFolders(); len(f) != 1 || f[0].Source != "plugin:hello-skill" {
		t.Fatalf("%+v", f)
	}
}

// 真的把 echo-mcp 装上、启动它的 MCP 服务、调用工具：整条链路（安装确认 → 展开配置 → 启动子进程 → 握手 → 工具注册 →
// 权限等级 → 调用）都是真的。需要本机有 python3，没有就跳过。
func TestExampleEchoMCPEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("没有 python3")
	}
	reg := tool.NewRegistry()
	mgr := mcp.NewManager(reg)
	defer mgr.Close()
	h := newExampleHost(t, mgr)

	// 先做预览：看到的是作者声明的风险和要运行的命令，此时没有任何工具。
	p, err := h.Preview(exampleDir("echo-mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Plugin.Servers[0].Declared != "read" || p.Plugin.Servers[0].Effective != "exec" || !strings.Contains(p.Plugin.Servers[0].Command, "echo_server.py") {
		t.Fatalf("%+v", p.Plugin.Servers)
	}
	if _, ok := reg.Get("mcp__echo-mcp__echo"); ok {
		t.Fatal("预览时不应注册工具")
	}
	if _, err := h.Install(p.Token, nil); err != nil { // 不勾选：按 exec 运行
		t.Fatal(err)
	}
	waitUntil(t, func() bool { _, ok := reg.Get("mcp__echo-mcp__echo"); return ok })
	echo, _ := reg.Get("mcp__echo-mcp__echo")
	if echo.Spec().Risk != "exec" {
		t.Fatalf("没确认的 read 声明应按 exec: %v", echo.Spec().Risk)
	}
	res := echo.Execute(context.Background(), tool.Call{Input: []byte(`{"text":"你好"}`)})
	if res.IsError || res.Output != "echo: 你好" {
		t.Fatalf("%+v", res)
	}

	// 改了配置：服务用新的前缀重启。
	if _, err := h.SetConfig("echo-mcp", map[string]string{"prefix": ">>", "loud": "true"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		e, ok := reg.Get("mcp__echo-mcp__echo")
		if !ok {
			return false
		}
		r := e.Execute(context.Background(), tool.Call{Input: []byte(`{"text":"hi"}`)})
		return r.Output == ">> HI"
	})

	// 状态检查和操作按钮。
	st, err := h.Status(context.Background(), "echo-mcp")
	if err != nil || st.State != StateOK {
		t.Fatalf("%v %+v", err, st)
	}
	act, err := h.RunAction(context.Background(), "echo-mcp", "selftest")
	if err != nil || act.ExitCode != 0 || !strings.Contains(act.Output, "自检通过") {
		t.Fatalf("%v %+v", err, act)
	}

	// 停用：工具和子进程一并消失。
	if _, err := h.SetEnabled("echo-mcp", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("mcp__echo-mcp__echo"); ok {
		t.Fatal("停用后工具应移除")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}
