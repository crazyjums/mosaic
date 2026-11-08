package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHooks 记录 Host 推给装配层的钩子。
type fakeHooks struct {
	got      map[string][]HookDecl
	dir      map[string]string
	disabled bool
}

func (f *fakeHooks) ApplyPluginHooks(id, name, dir, dataDir string, hs []HookDecl) {
	if f.got == nil {
		f.got, f.dir = map[string][]HookDecl{}, map[string]string{}
	}
	f.got[id], f.dir[id] = hs, dir
}
func (f *fakeHooks) SetHooksDisabled(off bool) { f.disabled = off }

func writeHookPlugin(t *testing.T, dir, command string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"guard"}`)
	writeFile(t, filepath.Join(dir, "hooks", "hooks.json"), `{"hooks":{
  "PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"`+command+`","timeout":5},{"type":"prompt","prompt":"x"}]}],
  "SessionStart":[{"hooks":[{"type":"command","command":"echo hi"}]}]
}}`)
}

func TestHookImportVariants(t *testing.T) {
	t.Run("清单里的文件、内联配置与默认文件合并；不支持的事件和类型如实告知", func(t *testing.T) {
		dir := t.TempDir()
		writeHookPlugin(t, dir, "./guard.sh")
		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"guard","hooks":["./extra/hooks.json",{"x":1}]}`)
		if _, err := ParseManifest(dir, "dev"); err != nil {
			// 数组里混了对象：整个 hooks 写法不支持，但默认文件照常导入
			t.Fatalf("%v", err)
		}
		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"guard","hooks":"./extra/hooks.json"}`)
		writeFile(t, filepath.Join(dir, "extra", "hooks.json"), `{"Stop":[{"hooks":[{"command":"./check.sh"}]}]}`)
		m, err := ParseManifest(dir, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if len(m.HookDecls) != 2 || m.HookDecls[0].Event != "PreToolUse" || m.HookDecls[0].Timeout != 5 || m.HookDecls[1].Event != "Stop" || m.HookDecls[1].File != "extra/hooks.json" {
			t.Fatalf("%+v", m.HookDecls)
		}
		ig := strings.Join(m.Ignored, "\n")
		if !strings.Contains(ig, "SessionStart") || !strings.Contains(ig, "prompt") {
			t.Fatalf("不支持的事件和类型要告知: %s", ig)
		}

		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"guard","hooks":{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"date"}]}]}}}`)
		m, _ = ParseManifest(dir, "dev")
		if len(m.HookDecls) != 2 || m.HookDecls[1].Event != "UserPromptSubmit" || m.HookDecls[1].File != "plugin.json" {
			t.Fatalf("内联配置: %+v", m.HookDecls)
		}

		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"guard","hooks":"../outside.json"}`)
		m, _ = ParseManifest(dir, "dev")
		if !strings.Contains(strings.Join(m.Ignored, "\n"), "outside.json") {
			t.Fatalf("指到插件外的文件要拒绝: %v", m.Ignored)
		}
	})
	t.Run("指纹：命令、匹配条件、时限变了就是另一条", func(t *testing.T) {
		a := hookFingerprint("PreToolUse", "Bash", "x", 0)
		for _, b := range []string{hookFingerprint("PreToolUse", "Bash", "y", 0), hookFingerprint("PreToolUse", "Write", "x", 0), hookFingerprint("PreToolUse", "Bash", "x", 5), hookFingerprint("PostToolUse", "Bash", "x", 0)} {
			if a == b {
				t.Fatal("指纹应不同")
			}
		}
	})
}

func TestHookApproval(t *testing.T) {
	src := t.TempDir()
	writeHookPlugin(t, src, "./guard.sh")
	h, _, _, root := newTestHost(t)
	fh := &fakeHooks{}
	h.opts.Hooks = fh

	pv, err := h.Preview(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Plugin.Hooks) != 1 || pv.Plugin.Hooks[0].Approved || pv.Plugin.Hooks[0].Command != "./guard.sh" {
		t.Fatalf("安装确认里逐条列出，默认没批准: %+v", pv.Plugin.Hooks)
	}
	hid := pv.Plugin.Hooks[0].ID

	// 不勾：装上了，但钩子不运行
	if _, err := h.Install(pv.Token, nil); err != nil {
		t.Fatal(err)
	}
	if len(fh.got["guard"]) != 0 {
		t.Fatalf("没批准的钩子不能运行: %+v", fh.got)
	}
	// 详情里打开
	info, err := h.SetHookApproved("guard", hid, true)
	if err != nil || !info.Hooks[0].Approved || len(fh.got["guard"]) != 1 || fh.dir["guard"] == "" {
		t.Fatalf("%+v %v %+v", info.Hooks, err, fh.got)
	}
	if _, err := h.SetHookApproved("guard", "nope", true); err == nil {
		t.Fatal("不存在的钩子应报错")
	}
	// 停用插件：不运行；再启用：恢复
	if _, err := h.SetEnabled("guard", false); err != nil || len(fh.got["guard"]) != 0 {
		t.Fatalf("停用后不运行: %v %+v", err, fh.got)
	}
	if _, err := h.SetEnabled("guard", true); err != nil || len(fh.got["guard"]) != 1 {
		t.Fatalf("%v %+v", err, fh.got)
	}
	// 总开关：保存下来，重启后仍然生效
	if err := h.SetHooksDisabled(true); err != nil || !fh.disabled || !h.HooksDisabled() {
		t.Fatal("总开关")
	}
	h2, err := NewHost(HostOptions{Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"), MCP: newFakeMCP(), Hooks: &fakeHooks{}, AppVersion: "1.3.2", LookPath: h.opts.LookPath})
	if err != nil || !h2.HooksDisabled() || !h2.opts.Hooks.(*fakeHooks).disabled {
		t.Fatalf("总开关要持久化: %v", err)
	}
	_ = h.SetHooksDisabled(false)

	// 更新：命令变了 → 指纹变了 → 需要重新批准
	src2 := t.TempDir()
	writeHookPlugin(t, src2, "./guard.sh --strict")
	pv2, err := h.Preview(src2)
	if err != nil {
		t.Fatal(err)
	}
	if pv2.Plugin.Hooks[0].ID == hid {
		t.Fatal("命令变了，指纹应变")
	}
	if _, err := h.Install(pv2.Token, []string{"hook:" + hid}); err == nil {
		t.Fatal("批准旧指纹应报错")
	}
	pv2, _ = h.Preview(src2)
	if _, err := h.Install(pv2.Token, nil); err != nil {
		t.Fatal(err)
	}
	if len(fh.got["guard"]) != 0 {
		t.Fatalf("更新后变了的钩子要重新批准: %+v", fh.got)
	}
	pv3, _ := h.Preview(src2)
	if _, err := h.Install(pv3.Token, []string{HookApprovalPrefix + pv3.Plugin.Hooks[0].ID}); err != nil {
		t.Fatal(err)
	}
	if len(fh.got["guard"]) != 1 || fh.got["guard"][0].Command != "./guard.sh --strict" {
		t.Fatalf("%+v", fh.got)
	}
	// 卸载：清空
	if err := h.Uninstall("guard", true); err != nil || len(fh.got["guard"]) != 0 {
		t.Fatalf("%v %+v", err, fh.got)
	}
	_ = os.RemoveAll(src)
}
