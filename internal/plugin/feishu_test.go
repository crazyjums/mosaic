package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/catalog"
	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/secret"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// 飞书插件的端到端测试。真正的 lark-cli 需要飞书租户和账号，测试里用一个假的 lark-cli（放在 PATH 里的脚本）：
// 它按子命令返回固定输出，并把收到的参数记进日志——验证的是插件**传给 lark-cli 什么**、**拒绝什么**，
// 以及登录流程的状态流转；对真实飞书的行为没有验证。
type fakeLark struct {
	bin string
	log string
}

func newFakeLark(t *testing.T) *fakeLark {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("假 lark-cli 是 shell 脚本")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("需要 python3")
	}
	bin := t.TempDir()
	f := &fakeLark{bin: bin, log: filepath.Join(bin, "calls.log")}
	script := `#!/bin/sh
printf '%s\n' "$*" >> '` + f.log + `'
case "$1 $2" in
"auth status")
  if [ -e '` + filepath.Join(bin, "loggedin") + `' ]; then echo '{"appId":"cli_x","brand":"feishu","identity":"user","userName":"小明","userOpenId":"ou_1"}'; else echo '{"appId":"cli_x","brand":"feishu","defaultAs":"auto","identity":"bot","note":"Token does not exist or has been cleared. Only bot (tenant) identity is available. Re-login: lark-cli auth login","userName":"小明","userOpenId":"ou_1"}'; fi; exit 0;;
"auth login")
  case "$*" in
  *--no-wait*) echo '{"ok":true,"data":{"verification_url":"https://accounts.feishu.cn/auth?x=1","device_code":"DEVCODE123456"}}'; exit 0;;
  *--device-code\ DEVCODE123456*) if [ -e '` + filepath.Join(bin, "slow") + `' ]; then sleep 20; fi; touch '` + filepath.Join(bin, "loggedin") + `'; echo '{"ok":true}'; exit 0;;
  esac; echo 'bad login args' >&2; exit 1;;
"config init") echo 'open https://accounts.example/config'; exit 0;;
esac
case "$*" in
*--help*) echo "help for: $*"; exit 0;;
esac
echo '{"ok":true,"data":{"echo":"'"$*"'"}}'
`
	if err := os.WriteFile(filepath.Join(bin, "lark-cli"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f *fakeLark) calls(t *testing.T) []string {
	data, _ := os.ReadFile(f.log)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestFeishuPlugin(t *testing.T) {
	fake := newFakeLark(t)
	reg := tool.NewRegistry()
	mgr := mcp.NewManager(reg)
	defer mgr.Close()
	root := t.TempDir()
	h, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: secret.NewMemory(), MCP: mgr, AppVersion: "1.6.0", Catalog: catalog.FS,
	})
	if err != nil {
		t.Fatal(err)
	}

	p, err := h.PreviewCatalog("feishu")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Plugin.Servers) != 1 || !p.Plugin.Servers[0].Sensitive || p.Plugin.Servers[0].Effective != "exec" {
		t.Fatalf("预览应如实告诉用户这是私密工具: %+v", p.Plugin.Servers)
	}
	if _, err := h.Install(p.Token, nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { _, ok := reg.Get("mcp__feishu__feishu_read"); return ok })
	read, _ := reg.Get("mcp__feishu__feishu_read")
	help, _ := reg.Get("mcp__feishu__feishu_help")
	if !read.Spec().Sensitive || !help.Spec().Sensitive {
		t.Fatal("两个工具都应是私密工具（每次都询问，不能被总是允许绕过）")
	}
	call := func(tl tool.Tool, in string) tool.Result {
		return tl.Execute(context.Background(), tool.Call{Input: []byte(in)})
	}

	// 允许的读取：参数原样传给 lark-cli，并强制加上用户身份。
	res := call(read, `{"command":"im +chat-messages-list","args":["--chat-id","oc_1","--page-limit","1"]}`)
	if res.IsError || !strings.Contains(res.Output, "im +chat-messages-list --chat-id oc_1 --page-limit 1 --as user") {
		t.Fatalf("%+v", res)
	}
	for _, ok := range []string{`"docs +fetch"`, `"im +chat-search"`, `"im +messages-search"`, `"drive +search"`, `"calendar +agenda"`, `"im +messages-mget"`} {
		if r := call(read, `{"command":`+ok+`,"args":[]}`); r.IsError {
			t.Errorf("%s 应允许: %+v", ok, r)
		}
	}

	// 写入类、未知业务域、原始 API、模型想改身份或写文件的参数，一律拒绝，而且根本不会去运行 lark-cli。
	before := len(fake.calls(t))
	for _, bad := range []string{
		`{"command":"im +messages-send","args":["--chat-id","oc_1","--text","hi"]}`,
		`{"command":"docs +create","args":[]}`,
		`{"command":"docs +fetch-and-delete","args":[]}`,
		`{"command":"im +chat-list-update","args":[]}`,
		`{"command":"api GET","args":["/open-apis/x"]}`,
		`{"command":"shell +list","args":[]}`,
		`{"command":"im chat-list","args":[]}`,
		`{"command":"im +chat-manage","args":[]}`,
		`{"command":"im +chat-list; rm -rf /","args":[]}`,
		`{"command":"im +chat-list","args":["--as","bot"]}`,
		`{"command":"im +chat-list","args":["--as=bot"]}`,
		`{"command":"docs +fetch","args":["--output","x.md"]}`,
		`{"command":"docs +fetch","args":["--output-dir=."]}`,
		`{"command":"docs +fetch","args":["@/etc/passwd"]}`,
		`{"command":"docs +fetch","args":["--yes"]}`,
		`{"command":"docs +fetch","args":"not-an-array"}`,
		`{"command":"docs +fetch","args":[1,2]}`,
	} {
		if r := call(read, bad); !r.IsError {
			t.Errorf("应被拒绝: %s -> %+v", bad, r)
		}
	}
	if after := len(fake.calls(t)); after != before {
		t.Fatalf("被拒绝的调用不应运行 lark-cli: %d -> %d", before, after)
	}

	// 帮助：只能查已知业务域。
	if r := call(help, `{"topic":"im +chat-messages-list"}`); r.IsError || !strings.Contains(r.Output, "help for: im +chat-messages-list --help") {
		t.Fatalf("%+v", r)
	}
	if r := call(help, `{"topic":"evil"}`); !r.IsError {
		t.Fatal("未知业务域应拒绝")
	}
	if r := call(help, `{"topic":"im; id"}`); !r.IsError {
		t.Fatal("带分号应拒绝")
	}

	// 登录流程：未登录 → 开始登录（授权链接、记下设备码）→ 完成 → 已登录。
	// 真实的“没有用户令牌”输出：identity 是 bot，即使 userName 还在、退出码是 0，也要判为未登录。
	st, err := h.Status(context.Background(), "feishu")
	if err != nil || st.State != StateBad || !strings.Contains(st.Message, "未登录") || !strings.Contains(st.Message, "Re-login") {
		t.Fatalf("没登录应是异常，并给出原因: %v %+v", err, st)
	}
	// 没开始登录就没有等待中的状态。
	if got := h.GetLoginState("feishu"); got.State != LoginIdle {
		t.Fatalf("%+v", got)
	}
	ls, err := h.StartLogin("feishu")
	if err != nil || ls.State != LoginRunning || ls.URL != "https://accounts.feishu.cn/auth?x=1" {
		t.Fatalf("应返回授权链接并进入等待: %v %+v", err, ls)
	}
	if _, err := os.Stat(filepath.Join(root, "plugin-data", "feishu", "pending-login.json")); err != nil {
		t.Fatalf("设备码应记在插件数据目录里: %v", err)
	}
	waitUntil(t, func() bool { return h.GetLoginState("feishu").State == LoginOK })
	if got := h.GetLoginState("feishu"); !strings.Contains(got.Message, "登录完成") || !strings.Contains(got.Message, "小明") {
		t.Fatalf("%+v", got)
	}
	if st, err := h.Status(context.Background(), "feishu"); err != nil || st.State != StateOK || !strings.Contains(st.Message, "已登录：小明") {
		t.Fatalf("登录后应正常: %v %+v", err, st)
	}
	if act, err := h.RunAction(context.Background(), "feishu", "init"); err != nil || !strings.Contains(act.Output, "https://accounts.example/config") {
		t.Fatalf("%v %+v", err, act)
	}
	joined := strings.Join(fake.calls(t), "\n")
	if !strings.Contains(joined, "auth login --recommend --no-wait") || !strings.Contains(joined, "auth login --device-code DEVCODE123456") {
		t.Fatalf("登录应按 --no-wait / --device-code 两步走:\n%s", joined)
	}
}

// 没有安装 lark-cli 时：依赖检查给出提示，状态是“缺少依赖”，而不是一个难懂的报错。
func TestFeishuMissingLarkCLI(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("需要 python3")
	}
	t.Setenv("PATH", t.TempDir()) // 空 PATH：找不到任何命令
	root := t.TempDir()
	h, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: secret.NewMemory(), MCP: newFakeMCP(), AppVersion: "1.6.0", Catalog: catalog.FS,
		LookPath: func(name string) (string, error) {
			if name == "python3" {
				return "/usr/bin/python3", nil
			}
			return "", os.ErrNotExist
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.PreviewCatalog("feishu")
	if err != nil {
		t.Fatal(err)
	}
	info, err := h.Install(p.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	missing := false
	for _, r := range info.Requires {
		if r.Command == "lark-cli" && !r.Found {
			missing = true
		}
	}
	if !missing {
		t.Fatalf("应提示缺少 lark-cli: %+v", info.Requires)
	}
	if st, _ := h.Status(context.Background(), "feishu"); st.State != StateMissingDependency {
		t.Fatalf("%+v", st)
	}
}

// 授权命令成功但没有拿到用户令牌（status 仍是 bot 身份）：不能报“登录完成”。
func TestFeishuLoginFinishWithoutToken(t *testing.T) {
	fake := newFakeLark(t)
	// 让假 lark-cli 的 device-code 步骤成功但不创建 loggedin 标记。
	script, _ := os.ReadFile(filepath.Join(fake.bin, "lark-cli"))
	patched := strings.Replace(string(script), "touch '"+filepath.Join(fake.bin, "loggedin")+"'; ", "", 1)
	if patched == string(script) {
		t.Fatal("假脚本没有被改写")
	}
	if err := os.WriteFile(filepath.Join(fake.bin, "lark-cli"), []byte(patched), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	mgr := mcp.NewManager(reg)
	defer mgr.Close()
	root := t.TempDir()
	h, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: secret.NewMemory(), MCP: mgr, AppVersion: "1.6.0", Catalog: catalog.FS,
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.PreviewCatalog("feishu")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Install(p.Token, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.StartLogin("feishu"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return h.GetLoginState("feishu").State == LoginFailed })
	if got := h.GetLoginState("feishu"); strings.Contains(got.Message, "登录完成") || !strings.Contains(got.Message, "仍然没有可用的用户令牌") {
		t.Fatalf("没拿到令牌时不应报登录完成: %+v", got)
	}
}

// 取消：后台等待被终止，状态是已取消，之后还能重新开始。
func TestFeishuLoginCancel(t *testing.T) {
	fake := newFakeLark(t)
	must(t, os.WriteFile(filepath.Join(fake.bin, "slow"), nil, 0o644))
	root := t.TempDir()
	h, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: secret.NewMemory(), MCP: newFakeMCP(), AppVersion: "1.6.0", Catalog: catalog.FS,
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.PreviewCatalog("feishu")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Install(p.Token, nil); err != nil {
		t.Fatal(err)
	}
	first, err := h.StartLogin("feishu")
	if err != nil || first.State != LoginRunning {
		t.Fatalf("%v %+v", err, first)
	}
	if again, _ := h.StartLogin("feishu"); again.State != LoginRunning || again.URL != first.URL {
		t.Fatalf("等待中再点应返回同一次登录，方便重新打开链接: %+v", again)
	}
	h.CancelLogin("feishu")
	if got := h.GetLoginState("feishu"); got.State != LoginCancelled {
		t.Fatalf("%+v", got)
	}
	time.Sleep(300 * time.Millisecond) // 后台进程被终止后不应把状态改回去
	if got := h.GetLoginState("feishu"); got.State != LoginCancelled {
		t.Fatalf("取消后状态不应变化: %+v", got)
	}
	if _, err := h.StartLogin("feishu"); err != nil {
		t.Fatalf("取消后应能重新开始: %v", err)
	}
	h.CancelLogin("feishu")
}
