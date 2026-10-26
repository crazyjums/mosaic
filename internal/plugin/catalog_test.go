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
)

func newCatalogHost(t *testing.T, registryURL string) (*Host, string) {
	t.Helper()
	root := t.TempDir()
	h, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: secret.NewMemory(), MCP: newFakeMCP(), AppVersion: "1.3.2",
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
	if len(list) != 2 || list[0].ID != "hello-skill" || list[0].Kind != "bundled" || list[0].Installed {
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
	if l := h.Catalog(); !l[0].Installed || l[0].InstalledVersion == "" {
		t.Fatalf("装好后应标记已安装: %+v", l[0])
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

	list, err := h.RefreshCatalog(context.Background())
	if err != nil || len(list) != 3 {
		t.Fatalf("%+v %v", list, err)
	}
	if list[0].Name != "覆盖内置" || list[2].ID != "remote-one" || list[2].Kind != "git" || list[2].Ref != "v1" {
		t.Fatalf("在线列表应覆盖同 id 的内置项并追加新项: %+v", list)
	}

	// 缓存下来：重启后（离线）仍能看到上次的列表。
	h2, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: secret.NewMemory(), MCP: newFakeMCP(), Catalog: catalog.FS,
	})
	if err != nil || len(h2.Catalog()) != 3 {
		t.Fatalf("缓存应在重启后生效: %v", err)
	}

	// 失败时保持原样。
	status, body = http.StatusInternalServerError, "oops"
	if _, err := h.RefreshCatalog(context.Background()); err == nil || len(h.Catalog()) != 3 {
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
