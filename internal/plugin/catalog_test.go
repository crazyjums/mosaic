package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/catalog"
	"github.com/crazyjums/myllmapp/internal/secret"
	"github.com/crazyjums/myllmapp/internal/skill"
)

func newCatalogHost(t *testing.T, registryURL string) (*Host, string) {
	t.Helper()
	root := t.TempDir()
	h, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: secret.NewMemory(), MCP: newFakeMCP(), AppVersion: "1.6.0",
		Catalog: catalog.FS, RegistryURL: registryURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, root
}

func TestCatalogBuiltinAndInstall(t *testing.T) {
	h, _ := newCatalogHost(t, "")
	list := h.Catalog()
	if e, ok := findEntry(list, "hello-skill"); len(list) < 2 || !ok || e.Kind != "bundled" || e.Installed {
		t.Fatalf("%+v", list)
	}
	p, err := h.PreviewCatalog("hello-skill")
	if err != nil {
		t.Fatal(err)
	}
	info, err := h.Install(p.Token, nil)
	if err != nil || info.Source != "bundled:hello-skill" || len(info.Commands) != 1 {
		t.Fatalf("%+v %v", info, err)
	}
	if e, _ := findEntry(h.Catalog(), "hello-skill"); !e.Installed || e.InstalledVersion == "" {
		t.Fatalf("装好后应标记已安装: %+v", e)
	}
	if e, _ := findEntry(h.Catalog(), "hello-skill"); e.Version == "" || e.UpdateAvailable {
		t.Fatalf("刚装好的版本和市场一致，不应提示更新: %+v", e)
	}
	// 市场里的版本和已安装的不同：提示更新；重新安装后恢复。
	manifest := filepath.Join(h.opts.Dir, "hello-skill", FileName)
	data, _ := os.ReadFile(manifest)
	must(t, os.WriteFile(manifest, []byte(strings.Replace(string(data), `"version": "0.1.0"`, `"version": "0.0.1"`, 1)), 0o644))
	h.mu.Lock()
	h.scan()
	h.mu.Unlock()
	if e, _ := findEntry(h.Catalog(), "hello-skill"); !e.UpdateAvailable || e.InstalledVersion != "0.0.1" {
		t.Fatalf("应提示更新: %+v", e)
	}
	if _, err := h.PreviewCatalog("nope"); err == nil {
		t.Fatal("不存在的 id 应报错")
	}
	if _, err := h.PreviewBundled("../etc"); err == nil {
		t.Fatal("内置插件名不能带路径")
	}
}

func TestValidateGit(t *testing.T) {
	ok := [][3]string{{"https://github.com/a/b.git", "", ""}, {"https://github.com/a/b", "v1.2.0", "plugins/x"}}
	for _, c := range ok {
		if err := validateGit(c[0], c[1], c[2]); err != nil {
			t.Errorf("%v 应通过: %v", c, err)
		}
	}
	bad := [][3]string{
		{"http://github.com/a/b", "", ""}, {"ssh://git@github.com/a/b", "", ""}, {"file:///etc", "", ""},
		{"-oProxyCommand=x", "", ""}, {"https://user:pw@github.com/a/b", "", ""}, {"https://", "", ""},
		{"https://github.com/a/b", "--upload-pack=x", ""}, {"https://github.com/a/b", "a..b", ""},
		{"https://github.com/a/b", "", "../x"}, {"https://github.com/a/b", "", "/abs"}, {"https://github.com/a b", "", ""},
	}
	for _, c := range bad {
		if validateGit(c[0], c[1], c[2]) == nil {
			t.Errorf("%v 应被拒绝", c)
		}
	}
}

func TestRegistryDropsBadEntries(t *testing.T) {
	h, _ := newCatalogHost(t, "")
	data := `{"version":1,"plugins":[
	 {"id":"good","name":"好","summary":{"zh":"x"},"git":"https://github.com/a/good"},
	 {"id":"bad-scheme","name":"x","git":"http://github.com/a/b"},
	 {"id":"both","name":"x","git":"https://github.com/a/b","bundled":"hello-skill"},
	 {"id":"missing-bundled","name":"x","bundled":"not-there"},
	 {"id":"Bad_ID","name":"x","git":"https://github.com/a/b"},
	 {"id":"good","name":"重复","git":"https://github.com/a/c"},
	 {"id":"noname","git":"https://github.com/a/b"}]}`
	got := h.parseRegistry([]byte(data))
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("%+v", got)
	}
	if h.parseRegistry([]byte(`{"version":2,"plugins":[]}`)) != nil || h.parseRegistry([]byte(`not json`)) != nil {
		t.Fatal("版本不对或不是 JSON 应整体忽略")
	}
}

func TestRefreshCatalog(t *testing.T) {
	body := `{"version":1,"plugins":[
	 {"id":"remote-one","name":"远程插件","summary":{"zh":"来自在线列表"},"git":"https://github.com/a/remote-one","ref":"v1","subdir":"p"},
	 {"id":"hello-skill","name":"覆盖内置","bundled":"hello-skill"}]}`
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	h, root := newCatalogHost(t, srv.URL)

	builtin := len(h.Catalog())
	list, err := h.RefreshCatalog(context.Background())
	if err != nil || len(list) != builtin+1 {
		t.Fatalf("%+v %v", list, err)
	}
	over, _ := findEntry(list, "hello-skill")
	one, ok := findEntry(list, "remote-one")
	if over.Name != "覆盖内置" || !ok || one.Kind != "git" || one.Ref != "v1" || list[len(list)-1].ID != "remote-one" {
		t.Fatalf("在线列表应覆盖同 id 的内置项并追加新项: %+v", list)
	}

	// 缓存下来：重启后（离线）仍能看到上次的列表。
	h2, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: secret.NewMemory(), MCP: newFakeMCP(), Catalog: catalog.FS,
	})
	if err != nil || len(h2.Catalog()) != builtin+1 {
		t.Fatalf("缓存应在重启后生效: %v", err)
	}

	// 失败时保持原样。
	status, body = http.StatusInternalServerError, "oops"
	if _, err := h.RefreshCatalog(context.Background()); err == nil || len(h.Catalog()) != builtin+1 {
		t.Fatalf("刷新失败应报错并保留原列表: %v", err)
	}
	status, body = http.StatusOK, `{"version":9}`
	if _, err := h.RefreshCatalog(context.Background()); err == nil {
		t.Fatal("格式不对应报错")
	}
}

// 用一个假的 git（放在 PATH 里的脚本）验证参数和目录处理：不联网，只检查我们传给 git 的东西，
// 以及克隆结果怎么进入安装流程。
func TestPreviewGitWithFakeGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假 git 是 shell 脚本")
	}
	repo := t.TempDir()
	plug := filepath.Join(repo, "plugins", "demo")
	must(t, os.MkdirAll(plug, 0o755))
	must(t, os.WriteFile(filepath.Join(plug, "plugin.json"), []byte(`{"id":"demo-git","name":"仓库插件","version":"0.1.0"}`), 0o644))
	must(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("x"), 0o644))

	bin := t.TempDir()
	argsFile := filepath.Join(bin, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\n" +
		"if [ -e '" + filepath.Join(bin, "fail") + "' ]; then echo 'fatal: repository not found' >&2; exit 128; fi\n" +
		"for last; do :; done\ncp -R '" + repo + "'/. \"$last\"\n"
	must(t, os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	h, _ := newCatalogHost(t, "")
	p, err := h.PreviewGit("https://github.com/a/b.git", "v1.0", "plugins/demo")
	if err != nil {
		t.Fatal(err)
	}
	if p.Plugin.ID != "demo-git" {
		t.Fatalf("%+v", p.Plugin)
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"--depth\n1", "--branch\nv1.0", "protocol.allow=never", "--\nhttps://github.com/a/b.git\n"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("git 参数缺少 %q:\n%s", want, args)
		}
	}
	info, err := h.Install(p.Token, nil)
	if err != nil || info.Source != "https://github.com/a/b.git#v1.0" {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(h.opts.Dir, "demo-git", ".git")); err == nil {
		t.Fatal("不应把 .git 带进插件目录")
	}
	if _, err := os.Stat(filepath.Join(h.opts.Dir, ".staging")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(h.opts.Dir, ".staging"))
		if len(entries) != 0 {
			t.Fatalf("临时目录没清理: %v", entries)
		}
	}

	// 子目录不存在、git 失败，都要给出明确原因且不留临时文件。
	if _, err := h.PreviewGit("https://github.com/a/b", "", "nope"); err == nil || !strings.Contains(err.Error(), "没有目录") {
		t.Fatalf("%v", err)
	}
	must(t, os.WriteFile(filepath.Join(bin, "fail"), nil, 0o644)) // 子进程只继承白名单环境变量，所以用文件当开关
	if _, err := h.PreviewGit("https://github.com/a/b", "", ""); err == nil || !strings.Contains(err.Error(), "repository not found") {
		t.Fatalf("%v", err)
	}
	// Preview 也认 Git 地址（# 后面是分支）：这里 git 失败，说明走到了克隆这一步。
	if _, err := h.Preview("https://github.com/a/b#main"); err == nil || !strings.Contains(err.Error(), "下载失败") {
		t.Fatalf("%v", err)
	}
}

func TestPreviewGitWithoutGit(t *testing.T) {
	h, _ := newCatalogHost(t, "")
	h.opts.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if _, err := h.PreviewGit("https://github.com/a/b", "", ""); err == nil || !strings.Contains(err.Error(), "没有找到 git") {
		t.Fatalf("%v", err)
	}
}

// 卡片上的信息：分类、精选、带来什么，都来自 registry.json 和内置插件自己的清单。
func TestCatalogCardInfo(t *testing.T) {
	h, _ := newCatalogHost(t, "")
	list := h.Catalog()
	fe, ok := findEntry(list, "feishu")
	if !ok || !fe.Featured || fe.Category != "office" || !fe.Sensitive || fe.Servers != 1 || fe.Skills != 1 || fe.Commands != 2 || len(fe.Requires) != 2 {
		t.Fatalf("%+v", fe)
	}
	if cr, _ := findEntry(list, "code-review"); cr.Featured || cr.Category != "dev" || cr.Sensitive || cr.Servers != 0 {
		t.Fatalf("%+v", cr)
	}
	for _, e := range list {
		if e.Category == "" {
			t.Errorf("%s 没有分类", e.ID)
		}
	}
	// 不认识的分类当作没有分类，但条目本身保留。
	got := h.parseRegistry([]byte(`{"version":1,"plugins":[{"id":"x","name":"X","git":"https://github.com/a/x","category":"weird","featured":true}]}`))
	if len(got) != 1 || got[0].Category != "" {
		t.Fatalf("%+v", got)
	}
}

func findEntry(list []CatalogEntry, id string) (CatalogEntry, bool) {
	for _, e := range list {
		if e.ID == id {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// 市场里每一个内置插件都必须能装、清单合法、命令能展开：它们是随应用发布的，坏一个就是发布事故。
func TestEveryBundledPluginInstalls(t *testing.T) {
	h, _ := newCatalogHost(t, "")
	list := h.Catalog()
	if len(list) < 8 {
		t.Fatalf("内置插件应不少于 8 个: %d", len(list))
	}
	for _, e := range list {
		if e.Kind != "bundled" {
			continue
		}
		p, err := h.PreviewCatalog(e.ID)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		if p.Plugin.ID != e.ID {
			t.Errorf("%s: 清单 id 是 %q，要和市场 id 一致", e.ID, p.Plugin.ID)
		}
		info, err := h.Install(p.Token, nil)
		if err != nil || info.Error != "" || !info.Enabled {
			t.Fatalf("%s: %+v %v", e.ID, info, err)
		}
		for _, c := range info.Commands {
			text, ok, err := h.ExpandCommand(c.Name, "示例参数")
			if err != nil || !ok || !strings.Contains(text, "示例参数") {
				t.Errorf("%s /%s 应能展开并带上参数: %q %v %v", e.ID, c.Name, text, ok, err)
			}
		}
	}
	// 每个技能文件夹都要真的被发现（SKILL.md 的 name 和 description 都写对了）。
	got := map[string]bool{}
	for _, sk := range skill.Discover(t.TempDir(), t.TempDir(), h.SkillFolders()...) {
		got[sk.Name] = true
		if sk.Description == "" {
			t.Errorf("技能 %s 缺少 description", sk.Name)
		}
	}
	for _, want := range []string{"code-review", "git-conventions", "writing-style", "bug-triage", "sql-tuning", "greeting", "feishu"} {
		if !got[want] {
			t.Errorf("没有发现技能 %s: %v", want, got)
		}
	}
	// 技能清单里不能有重名；命令名也不能和别的插件冲突（冲突会让其中一个要加前缀才能用）。
	seen := map[string]string{}
	for _, c := range h.Commands() {
		if prev, dup := seen[c.Name]; dup {
			t.Errorf("命令 /%s 在 %s 和 %s 里重名", c.Name, prev, c.PluginID)
		}
		seen[c.Name] = c.PluginID
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// 脚手架生成的插件必须马上就能装、命令能用；已存在的目录不能被覆盖。
func TestScaffoldInstallsAsIs(t *testing.T) {
	h, _ := newCatalogHost(t, "")
	parent := t.TempDir()
	dir, err := Scaffold(parent, "my-plugin")
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.Preview(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := h.Install(p.Token, nil)
	if err != nil || len(info.Skills) != 1 || len(info.Commands) != 1 {
		t.Fatalf("%+v %v", info, err)
	}
	if text, ok, _ := h.ExpandCommand("my-plugin", "你好"); !ok || !strings.Contains(text, "你好") {
		t.Fatalf("%q %v", text, ok)
	}
	if _, err := Scaffold(parent, "my-plugin"); err == nil || !strings.Contains(err.Error(), "已经存在") {
		t.Fatalf("不应覆盖已有目录: %v", err)
	}
	for _, bad := range []string{"", "Bad_ID", "../x", "a/b", strings.Repeat("a", 25)} {
		if _, err := Scaffold(parent, bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
	if _, err := Scaffold(filepath.Join(parent, "nope"), "ok"); err == nil {
		t.Fatal("目录不存在应报错")
	}
}
