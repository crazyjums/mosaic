package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/permission"
)

// TestHelperProcess 让测试二进制自己充当“状态检查 / 操作按钮”要运行的外部命令：
// 命令行是  <测试二进制> -test.run=^TestHelperProcess$ -- <模式> [参数…]。直接 go test 时没有 "--"，什么都不做。
func TestHelperProcess(t *testing.T) {
	args := os.Args
	i := -1
	for k, a := range args {
		if a == "--" {
			i = k
			break
		}
	}
	if i < 0 {
		return
	}
	switch args[i+1] {
	case "ok-json":
		fmt.Print(`{"ok":true,"user":"ann"}`)
	case "bad-json":
		fmt.Print(`{"ok":false}`)
	case "plain-ok":
		fmt.Print("all good")
	case "exit3":
		fmt.Print("boom")
		os.Exit(3)
	case "env":
		fmt.Printf("PLUGIN_DIR=%s DATA_DIR=%s LEAK=%q", os.Getenv("PLUGIN_DIR"), os.Getenv("DATA_DIR"), os.Getenv("MOSAIC_TEST_SECRET"))
	case "args":
		fmt.Print(strings.Join(args[i+2:], "|"))
	case "sleep":
		time.Sleep(10 * time.Second)
	case "big":
		fmt.Print(strings.Repeat("x", maxRunOutput*2))
	}
	os.Exit(0)
}

func helperCmd(mode string, args ...string) string {
	parts := []string{`"` + os.Args[0] + `"`, "-test.run=^TestHelperProcess$", "--", mode}
	return strings.Join(append(parts, args...), " ")
}

func installDir(t *testing.T, h *Host, dir string, approve ...string) Info {
	t.Helper()
	p, err := h.Preview(dir)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	info, err := h.Install(p.Token, approve)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	return info
}

func TestInstallFromDirAndEnable(t *testing.T) {
	h, f, mem, root := newTestHost(t)
	src := t.TempDir()
	writePlugin(t, src, baseManifest("demo"))

	p, err := h.Preview(src)
	if err != nil {
		t.Fatal(err)
	}
	if p.Plugin.ID != "demo" || p.Existing != nil || len(p.Plugin.Servers) != 1 || p.Plugin.Servers[0].Declared != "read" || p.Plugin.Servers[0].Approved || p.Plugin.Servers[0].Effective != "exec" {
		t.Fatalf("预览: %+v", p.Plugin)
	}
	mustContain(t, p.Plugin.Servers[0].Command, "node ${PLUGIN_DIR}/server.js")
	if len(h.List()) != 0 || len(f.get("plugin:demo")) != 0 {
		t.Fatal("预览时什么都不应生效")
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "demo")); err == nil {
		t.Fatal("预览不应写入插件目录")
	}

	info, err := h.Install(p.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Enabled || info.Skills[0] != "docs" || info.Commands[0].Name != "sum" {
		t.Fatalf("安装后: %+v", info)
	}
	// token 只能用一次
	if _, err := h.Install(p.Token, nil); err == nil {
		t.Fatal("重复安装同一个 token 应失败")
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "demo", FileName)); err != nil {
		t.Fatal("插件应复制到插件目录")
	}

	// 必填的 token 还没填：MCP 服务不启动，界面上有提示。
	if len(f.get("plugin:demo")) != 0 {
		t.Fatalf("缺必填配置时不应启动服务: %+v", f.get("plugin:demo"))
	}
	if got, _ := h.Get("demo"); !strings.Contains(got.Warning, "令牌") {
		t.Fatalf("应提示缺哪一项: %q", got.Warning)
	}

	// 填好之后：变量展开、密钥来自钥匙串、风险没确认按 exec。
	if _, err := h.SetConfig("demo", map[string]string{"token": "s3cret", "profile": "work"}); err != nil {
		t.Fatal(err)
	}
	cfgs := f.get("plugin:demo")
	if len(cfgs) != 1 {
		t.Fatalf("应启动一个服务: %+v", cfgs)
	}
	c := cfgs[0]
	pluginDir := filepath.Join(root, "plugins", "demo")
	if c.Name != "demo" || c.Command != "node" || c.Args[0] != filepath.Join(pluginDir, "server.js") || c.Args[2] != "work" ||
		c.Env["TOKEN"] != "s3cret" || c.Env["DATA"] != filepath.Join(root, "plugin-data", "demo") || c.Cwd != pluginDir || c.Risk != permission.RiskExec {
		t.Fatalf("展开后的配置: %+v", c)
	}
	if v, _ := mem.Get("plugin/demo/token"); v != "s3cret" {
		t.Fatal("密钥应存进钥匙串")
	}
	// secret 不会出现在返回给界面的 Info 里，也不会写进状态文件。
	got, _ := h.Get("demo")
	for _, cv := range got.Config {
		if cv.Key == "token" && (cv.Value != "" || !cv.HasSecret) {
			t.Fatalf("secret 只显示是否已设置: %+v", cv)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "plugins.json")); strings.Contains(string(b), "s3cret") {
		t.Fatal("密钥不能写进状态文件")
	}

	// 停用：服务被摘掉，技能和命令消失；启用后回来。
	if _, err := h.SetEnabled("demo", false); err != nil {
		t.Fatal(err)
	}
	if len(f.get("plugin:demo")) != 0 || len(h.SkillFolders()) != 0 || len(h.Commands()) != 0 {
		t.Fatal("停用后服务、技能、命令都应消失")
	}
	if _, err := h.SetEnabled("demo", true); err != nil {
		t.Fatal(err)
	}
	if len(f.get("plugin:demo")) != 1 || len(h.SkillFolders()) != 1 || len(h.Commands()) != 1 {
		t.Fatal("启用后应恢复")
	}
}

func TestRiskApproval(t *testing.T) {
	h, f, _, _ := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("demo")
	m["config"] = nil
	m["mcp"] = []any{map[string]any{"command": "node", "risk": "read"}, map[string]any{"name": "w", "command": "node", "risk": "write"}}
	writePlugin(t, src, m)

	info := installDir(t, h, src, "demo")
	risk := map[string]permission.Risk{}
	for _, c := range f.get("plugin:demo") {
		risk[c.Name] = c.Risk
	}
	if risk["demo"] != permission.RiskRead || risk["demo-w"] != permission.RiskExec {
		t.Fatalf("只有确认过的才降级: %+v", risk)
	}
	if !info.Servers[0].Approved || info.Servers[1].Approved || info.Servers[1].Effective != "exec" {
		t.Fatalf("Info 里反映确认情况: %+v", info.Servers)
	}

	// 不能“确认”一个没声明的服务。
	p, _ := h.Preview(src)
	if _, err := h.Install(p.Token, []string{"nope"}); err == nil {
		t.Fatal("确认了没声明的服务应失败")
	}
}

func TestOverwriteKeepsConfigResetsApproval(t *testing.T) {
	h, f, _, _ := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("demo")
	m["mcp"] = []any{map[string]any{"command": "node", "risk": "read"}}
	writePlugin(t, src, m)
	installDir(t, h, src, "demo")
	if _, err := h.SetConfig("demo", map[string]string{"token": "t", "profile": "keep-me"}); err != nil {
		t.Fatal(err)
	}
	if f.get("plugin:demo")[0].Risk != permission.RiskRead {
		t.Fatal("前提：已确认")
	}

	m["version"] = "0.2.0"
	writePlugin(t, src, m)
	p, err := h.Preview(src)
	if err != nil || p.Existing == nil || p.Existing.Version != "0.1.0" || p.Plugin.Version != "0.2.0" {
		t.Fatalf("应识别为覆盖更新: %v %+v", err, p)
	}
	info, err := h.Install(p.Token, nil) // 这次没有勾选
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.2.0" {
		t.Fatalf("版本应更新: %+v", info)
	}
	for _, cv := range info.Config {
		if cv.Key == "profile" && cv.Value != "keep-me" {
			t.Fatalf("配置应保留: %+v", cv)
		}
	}
	if f.get("plugin:demo")[0].Risk != permission.RiskExec {
		t.Fatal("更新后风险确认要重来：没勾选就按 exec")
	}
}

func TestInstallFromZip(t *testing.T) {
	h, _, _, root := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("zipped")
	m["config"], m["mcp"] = nil, nil
	writePlugin(t, src, m)

	// 内容在最外层，或者在唯一的一层子目录里（GitHub 下载的 zip 就是这样）。
	for name, prefix := range map[string]string{"flat": "", "nested": "zipped-main/"} {
		z := filepath.Join(t.TempDir(), name+".zip")
		makeZip(t, src, z, prefix, nil)
		p, err := h.Preview(z)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.Plugin.ID != "zipped" {
			t.Fatalf("%s: %+v", name, p)
		}
		info, err := h.Install(p.Token, nil)
		if err != nil || !info.Enabled {
			t.Fatalf("%s: %v %+v", name, err, info)
		}
		if _, err := os.Stat(filepath.Join(root, "plugins", "zipped", "skills", "docs", "SKILL.md")); err != nil {
			t.Fatalf("%s: 文件应解压到位: %v", name, err)
		}
	}
}

func TestZipRejectsMaliciousArchives(t *testing.T) {
	h, _, _, root := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("evil")
	m["config"], m["mcp"] = nil, nil
	writePlugin(t, src, m)

	cases := map[string]map[string]string{
		"zip slip": {"../../escape.txt": "x"},
		"绝对路径":     {"/tmp/abs.txt": "x"},
		"反斜杠路径":    {`..\..\win.txt`: "x"},
		"盘符路径":     {`C:/x.txt`: "x"},
		"超大文件":     {"big.bin": strings.Repeat("a", maxPackFileSize+10)},
	}
	for name, extra := range cases {
		z := filepath.Join(t.TempDir(), "x.zip")
		makeZip(t, src, z, "", extra)
		if _, err := h.Preview(z); err == nil {
			t.Errorf("%s: 应被拒绝", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); err == nil {
		t.Fatal("zip slip 不能写到目录外")
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "plugins", ".staging")); len(entries) != 0 {
		t.Fatalf("失败的预览不应留下临时目录: %v", entries)
	}
	if _, err := h.Preview(filepath.Join(t.TempDir(), "nothing.zip")); err == nil {
		t.Fatal("文件不存在")
	}
	notZip := filepath.Join(t.TempDir(), "a.zip")
	writeFile(t, notZip, "not a zip")
	if _, err := h.Preview(notZip); err == nil {
		t.Fatal("不是 zip")
	}
	txt := filepath.Join(t.TempDir(), "a.txt")
	writeFile(t, txt, "x")
	if _, err := h.Preview(txt); err == nil {
		t.Fatal("既不是目录也不是 zip")
	}
}

func TestDirectoryInstallRejectsSymlinks(t *testing.T) {
	h, _, _, _ := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("linky")
	m["config"], m["mcp"] = nil, nil
	writePlugin(t, src, m)
	if err := os.Symlink(t.TempDir(), filepath.Join(src, "link")); err != nil {
		t.Skip("不能创建符号链接")
	}
	if _, err := h.Preview(src); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("目录里有符号链接应被拒绝: %v", err)
	}
}

func TestCancelPreviewAndRestart(t *testing.T) {
	h, f, _, root := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("demo")
	m["config"], m["mcp"] = nil, []any{map[string]any{"command": "node"}}
	writePlugin(t, src, m)

	p, err := h.Preview(src)
	if err != nil {
		t.Fatal(err)
	}
	h.CancelPreview(p.Token)
	if _, err := h.Install(p.Token, nil); err == nil {
		t.Fatal("取消后不能再安装")
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", ".staging", p.Token)); err == nil {
		t.Fatal("取消应清理临时目录")
	}

	installDir(t, h, src)
	if _, err := h.SetEnabled("demo", false); err != nil {
		t.Fatal(err)
	}
	// 留一个没完成的安装，重启时被清理。
	left := filepath.Join(root, "plugins", ".staging", "left")
	writeFile(t, filepath.Join(left, "x"), "x")

	// “重启”：新建一个 Host，状态来自磁盘。
	f2 := newFakeMCP()
	h2, err := NewHost(HostOptions{Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"), MCP: f2, AppVersion: "1.3.2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left); err == nil {
		t.Fatal("启动时应清理遗留的临时目录")
	}
	list := h2.List()
	if len(list) != 1 || list[0].Enabled || len(h2.Commands()) != 0 {
		t.Fatalf("停用状态应保留: %+v", list)
	}
	if _, err := h2.SetEnabled("demo", true); err != nil || len(h2.Commands()) != 1 {
		t.Fatalf("重启后启用: %v", err)
	}
	_ = f
}

func TestBadStateFileAndBrokenPlugin(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plugins.json"), "{坏了")
	good := filepath.Join(root, "plugins", "good")
	writePlugin(t, good, func() map[string]any { m := baseManifest("good"); m["config"], m["mcp"] = nil, nil; return m }())
	writeFile(t, filepath.Join(root, "plugins", "broken", FileName), "{不是 json")
	newer := filepath.Join(root, "plugins", "newer")
	writePlugin(t, newer, func() map[string]any {
		m := baseManifest("newer")
		m["min_app_version"] = "9.9.9"
		m["config"], m["mcp"] = nil, nil
		return m
	}())
	wrong := filepath.Join(root, "plugins", "wrongdir")
	writePlugin(t, wrong, func() map[string]any { m := baseManifest("other"); m["config"], m["mcp"] = nil, nil; return m }())

	h, err := NewHost(HostOptions{Dir: filepath.Join(root, "plugins"), StateFile: filepath.Join(root, "plugins.json"), MCP: newFakeMCP(), AppVersion: "1.3.2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins.json.bad")); err != nil {
		t.Fatal("坏掉的状态文件应留一份备份")
	}
	byID := map[string]Info{}
	for _, i := range h.List() {
		byID[i.ID] = i
	}
	if byID["good"].Error != "" || byID["broken"].Error == "" || !strings.Contains(byID["newer"].Error, "升级应用") || byID["other"].Error == "" {
		t.Fatalf("单个插件的问题只影响它自己: %+v", byID)
	}
	if _, err := h.SetEnabled("newer", true); err == nil {
		t.Fatal("不兼容的插件不能启用")
	}
	if _, err := h.SetEnabled("good", true); err != nil {
		t.Fatal(err)
	}
}

func TestSetConfigValidation(t *testing.T) {
	h, _, mem, _ := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("demo")
	m["config"] = append(m["config"].([]any), map[string]any{"key": "flag", "label": "开关", "type": "bool"})
	writePlugin(t, src, m)
	installDir(t, h, src)

	bad := []map[string]string{{"nope": "x"}, {"region": "mars"}, {"flag": "yes"}, {"profile": strings.Repeat("a", 5000)}}
	for _, v := range bad {
		if _, err := h.SetConfig("demo", v); err == nil {
			t.Errorf("%v 应被拒绝", v)
		}
	}
	if _, err := h.SetConfig("demo", map[string]string{"region": "us", "flag": "true", "token": "t1"}); err != nil {
		t.Fatal(err)
	}
	// secret 传空串表示不修改。
	if _, err := h.SetConfig("demo", map[string]string{"token": ""}); err != nil {
		t.Fatal(err)
	}
	if v, _ := mem.Get("plugin/demo/token"); v != "t1" {
		t.Fatal("空串不应覆盖已有密钥")
	}
	info, err := h.ClearSecret("demo", "token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Get("plugin/demo/token"); err == nil {
		t.Fatal("清除后钥匙串里不应还有")
	}
	for _, cv := range info.Config {
		if cv.Key == "token" && cv.HasSecret {
			t.Fatal("清除后应显示未设置")
		}
		if cv.Key == "region" && cv.Value != "us" {
			t.Fatalf("region: %+v", cv)
		}
	}
	if _, err := h.SetConfig("none", nil); err == nil {
		t.Fatal("没有这个插件")
	}
}

func TestSecretsUnavailable(t *testing.T) {
	root := t.TempDir()
	h, err := NewHost(HostOptions{Dir: filepath.Join(root, "plugins"), StateFile: filepath.Join(root, "plugins.json"), MCP: newFakeMCP(), AppVersion: "1.3.2"})
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	writePlugin(t, src, baseManifest("demo"))
	installDir(t, h, src)
	if _, err := h.SetConfig("demo", map[string]string{"token": "x"}); err == nil || !strings.Contains(err.Error(), "钥匙串") {
		t.Fatalf("没有钥匙串不能保存密钥（不会退回写文件）: %v", err)
	}
}

func TestUninstall(t *testing.T) {
	h, f, mem, root := newTestHost(t)
	src := t.TempDir()
	writePlugin(t, src, baseManifest("demo"))
	installDir(t, h, src)
	if _, err := h.SetConfig("demo", map[string]string{"token": "t"}); err != nil {
		t.Fatal(err)
	}
	if len(f.get("plugin:demo")) != 1 {
		t.Fatal("前提：服务已启动")
	}
	if err := h.Uninstall("demo", true); err != nil {
		t.Fatal(err)
	}
	if len(f.get("plugin:demo")) != 0 || len(h.List()) != 0 || len(h.Commands()) != 0 {
		t.Fatal("卸载后一切应消失")
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "demo")); err == nil {
		t.Fatal("插件目录应删除")
	}
	if _, err := os.Stat(filepath.Join(root, "plugin-data", "demo")); err == nil {
		t.Fatal("purge 应清除插件数据")
	}
	if _, err := mem.Get("plugin/demo/token"); err == nil {
		t.Fatal("purge 应清除钥匙串里的密钥")
	}

	// 不 purge：配置和密钥保留，重装后还在。
	installDir(t, h, src)
	if _, err := h.SetConfig("demo", map[string]string{"token": "t2", "profile": "p"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Uninstall("demo", false); err != nil {
		t.Fatal(err)
	}
	if v, _ := mem.Get("plugin/demo/token"); v != "t2" {
		t.Fatal("不 purge 时密钥应保留")
	}
	info := installDir(t, h, src)
	for _, cv := range info.Config {
		if cv.Key == "profile" && cv.Value != "p" || cv.Key == "token" && !cv.HasSecret {
			t.Fatalf("重装后应沿用之前的配置: %+v", cv)
		}
	}
	if err := h.Uninstall("nope", false); err == nil {
		t.Fatal("没有这个插件")
	}
}

func TestMCPConflictAndFailureSurface(t *testing.T) {
	h, f, _, _ := newTestHost(t)
	f.fail["plugin:demo"] = fmt.Errorf("mcp: server 名字已被占用: demo")
	src := t.TempDir()
	m := baseManifest("demo")
	m["config"], m["mcp"] = nil, []any{map[string]any{"command": "node"}}
	writePlugin(t, src, m)
	installDir(t, h, src)
	if got, _ := h.Get("demo"); !strings.Contains(got.Warning, "已被占用") {
		t.Fatalf("冲突应显示在插件的提示里: %+v", got)
	}
}

func TestCommandsConflictAndExpand(t *testing.T) {
	h, _, _, _ := newTestHost(t)
	for _, id := range []string{"alpha", "beta"} {
		src := t.TempDir()
		m := baseManifest(id)
		m["config"], m["mcp"] = nil, nil
		m["commands"] = []any{map[string]any{"name": "sum", "path": "commands/sum.md"}, map[string]any{"name": id + "-only", "path": "commands/sum.md", "description": "独有"}}
		writePlugin(t, src, m)
		installDir(t, h, src)
	}
	var names []string
	for _, c := range h.Commands() {
		names = append(names, c.Name)
	}
	want := "alpha:sum,alpha-only,beta:sum,beta-only"
	if strings.Join(names, ",") != want {
		t.Fatalf("重名的加插件前缀，独有的保持原名: %v", names)
	}
	text, ok, err := h.ExpandCommand("/Alpha:Sum", " 这篇文档 ")
	if err != nil || !ok || text != "请总结：这篇文档" {
		t.Fatalf("展开: %q %v %v", text, ok, err)
	}
	if _, ok, _ := h.ExpandCommand("sum", "x"); ok {
		t.Fatal("重名的命令不能用裸名字（有歧义）")
	}
	if _, ok, _ := h.ExpandCommand("alpha-only", ""); !ok {
		t.Fatal("独有的命令可以直接用")
	}
	if _, ok, _ := h.ExpandCommand("nothing", ""); ok {
		t.Fatal("不存在的命令 ok 应为 false，调用方照常发送原文")
	}
	// 停用一个插件后，另一个的命令不再需要前缀。
	if _, err := h.SetEnabled("beta", false); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := h.ExpandCommand("sum", "x"); !ok {
		t.Fatal("没有歧义后可以用裸名字")
	}
}

func TestStatusAndActions(t *testing.T) {
	h, _, _, _ := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("demo")
	m["config"], m["mcp"] = []any{map[string]any{"key": "who", "label": "谁", "type": "string", "default": "a b"}}, nil
	m["status"] = map[string]any{"command": helperCmd("ok-json"), "ok_path": "ok"}
	m["actions"] = []any{
		map[string]any{"id": "env", "label": "环境", "command": helperCmd("env")},
		map[string]any{"id": "args", "label": "参数", "command": helperCmd("args", "${config.who}", "${PLUGIN_DIR}")},
		map[string]any{"id": "fail", "label": "失败", "command": helperCmd("exit3")},
		map[string]any{"id": "slow", "label": "慢", "command": helperCmd("sleep")},
		map[string]any{"id": "big", "label": "大", "command": helperCmd("big")},
		map[string]any{"id": "missing", "label": "不存在", "command": "definitely-not-a-command-xyz"},
	}
	writePlugin(t, src, m)
	installDir(t, h, src)
	ctx := context.Background()

	st, err := h.Status(ctx, "demo")
	if err != nil || st.State != StateOK {
		t.Fatalf("状态: %v %+v", err, st)
	}

	// 变量的值有空格，仍是同一个参数；子进程拿到 PLUGIN_DIR / DATA_DIR，拿不到应用的环境。
	t.Setenv("MOSAIC_TEST_SECRET", "leaked")
	res, _ := h.RunAction(ctx, "demo", "env")
	mustContain(t, res.Output, "PLUGIN_DIR=")
	mustContain(t, res.Output, `LEAK=""`)
	res, _ = h.RunAction(ctx, "demo", "args")
	if !strings.HasPrefix(res.Output, "a b|") || strings.Count(res.Output, "|") != 1 {
		t.Fatalf("参数边界: %q", res.Output)
	}
	res, _ = h.RunAction(ctx, "demo", "fail")
	if res.ExitCode != 3 || res.Output != "boom" || res.Error != "" {
		t.Fatalf("非零退出码: %+v", res)
	}
	res, _ = h.RunAction(ctx, "demo", "missing")
	if res.Error == "" {
		t.Fatalf("找不到程序应报错: %+v", res)
	}
	res, _ = h.RunAction(ctx, "demo", "big")
	if len(res.Output) > maxRunOutput+100 || !strings.Contains(res.Output, "截断") {
		t.Fatalf("输出应被截断: %d", len(res.Output))
	}
	if _, err := h.RunAction(ctx, "demo", "nope"); err == nil {
		t.Fatal("没有这个操作")
	}
	if _, err := h.RunAction(ctx, "nope", "env"); err == nil {
		t.Fatal("没有这个插件")
	}

	// 取消 / 超时。
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, _ = h.RunAction(cctx, "demo", "slow")
	if res.Error == "" || time.Since(start) > 5*time.Second {
		t.Fatalf("被取消的操作应很快返回: %+v %v", res, time.Since(start))
	}
}

func TestStatusStates(t *testing.T) {
	for name, c := range map[string]struct {
		status map[string]any
		want   string
	}{
		"字段为 false":    {map[string]any{"command": helperCmd("bad-json"), "ok_path": "ok"}, StateBad},
		"没有指定字段，退出码 0": {map[string]any{"command": helperCmd("plain-ok")}, StateOK},
		"退出码非 0":       {map[string]any{"command": helperCmd("exit3")}, StateBad},
		"字段不存在":        {map[string]any{"command": helperCmd("ok-json"), "ok_path": "nope"}, StateUnknown},
		"命令不存在":        {map[string]any{"command": "definitely-not-a-command-xyz"}, StateUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			h, _, _, _ := newTestHost(t)
			src := t.TempDir()
			m := baseManifest("demo")
			m["config"], m["mcp"], m["status"] = nil, nil, c.status
			writePlugin(t, src, m)
			installDir(t, h, src)
			if st, _ := h.Status(context.Background(), "demo"); st.State != c.want {
				t.Fatalf("%+v", st)
			}
		})
	}

	h, _, _, _ := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("demo")
	m["config"], m["mcp"] = nil, nil
	m["requires"] = map[string]any{"commands": []any{"present-cmd", "absent-cmd"}}
	m["status"] = map[string]any{"command": helperCmd("ok-json")}
	m["actions"] = []any{map[string]any{"id": "go", "label": "去", "command": helperCmd("ok-json")}}
	writePlugin(t, src, m)
	info := installDir(t, h, src)
	if len(info.Requires) != 2 || !info.Requires[0].Found || info.Requires[1].Found {
		t.Fatalf("依赖检查: %+v", info.Requires)
	}
	st, _ := h.Status(context.Background(), "demo")
	if st.State != StateMissingDependency || len(st.Missing) != 1 || st.Missing[0] != "absent-cmd" {
		t.Fatalf("缺依赖时不运行命令，直接说缺什么: %+v", st)
	}
	if res, _ := h.RunAction(context.Background(), "demo", "go"); !strings.Contains(res.Error, "absent-cmd") {
		t.Fatalf("缺依赖时操作也不运行: %+v", res)
	}

	h2, _, _, _ := newTestHost(t)
	src2 := t.TempDir()
	m2 := baseManifest("plain")
	m2["config"], m2["mcp"] = nil, nil
	writePlugin(t, src2, m2)
	installDir(t, h2, src2)
	if st, _ := h2.Status(context.Background(), "plain"); st.State != StateNone {
		t.Fatalf("没有声明状态检查: %+v", st)
	}
}

func TestSkillFoldersFollowEnabled(t *testing.T) {
	h, _, _, _ := newTestHost(t)
	src := t.TempDir()
	m := baseManifest("demo")
	m["config"], m["mcp"] = nil, nil
	writePlugin(t, src, m)
	installDir(t, h, src)
	folders := h.SkillFolders()
	if len(folders) != 1 || folders[0].Source != "plugin:demo" || folders[0].Origin != "演示 demo" || filepath.Base(folders[0].Dir) != "docs" {
		t.Fatalf("%+v", folders)
	}
}
