package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeMarketplace 写一个按 Claude Code 官方文档结构的 marketplace：根目录有 .claude-plugin/marketplace.json，
// 插件放在 plugins/ 下。
func writeMarketplace(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), `{
  "name": "team-tools",
  "owner": {"name": "Example Team", "email": "dev@example.com"},
  "description": "Plugins for the team",
  "metadata": {"pluginRoot": "./plugins"},
  "plugins": [
    {"name": "formatter", "source": "./plugins/formatter", "description": "Formats code", "category": "dev"},
    {"name": "bare", "source": "bare", "description": "Resolved under pluginRoot"},
    {"name": "native", "source": "./plugins/native"},
    {"name": "remote-gh", "source": {"source": "github", "repo": "acme/remote-gh", "ref": "v2.0.0"}},
    {"name": "mono", "source": {"source": "git-subdir", "url": "acme/monorepo", "path": "tools/mono"}},
    {"name": "pinned", "source": {"source": "url", "url": "https://git.example.com/acme/pinned.git", "ref": "main", "sha": "0123456789abcdef0123456789abcdef01234567"}},
    {"name": "evil", "source": "./../outside"},
    {"name": "slash", "source": "plugins/no-dot"},
    {"name": "missing", "source": "./plugins/does-not-exist"},
    {"name": "plain-http", "source": {"source": "url", "url": "http://git.example.com/x.git"}},
    {"name": "ssh", "source": {"source": "url", "url": "git@github.com:acme/x.git"}},
    {"name": "bad-repo", "source": {"source": "github", "repo": "not a repo"}},
    {"name": "bad-sha", "source": {"source": "url", "url": "https://git.example.com/x.git", "sha": "abc"}},
    {"name": "from-npm", "source": {"source": "npm", "package": "@acme/x"}},
    {"name": "from-zip", "source": {"source": "archive", "url": "https://x.example/p.zip"}},
    {"name": "from-cmd", "source": {"source": "command", "command": "my-tool path"}},
    {"name": "formatter", "source": "./plugins/bare"},
    {"name": "bad name!", "source": "./plugins/bare"}
  ]
}`)
	// Claude Code 布局的插件
	writeFile(t, filepath.Join(root, "plugins", "formatter", "commands", "fmt.md"), "---\ndescription: Format\n---\nFormat $ARGUMENTS")
	writeFile(t, filepath.Join(root, "plugins", "bare", "skills", "s", "SKILL.md"), "---\nname: s\n---\nbody")
	// 原生 Mosaic 插件
	writePlugin(t, filepath.Join(root, "plugins", "native"), baseManifest("native"))
}

func TestMarketplaceListing(t *testing.T) {
	root := t.TempDir()
	writeMarketplace(t, root)
	h, _, _, _ := newTestHost(t)
	in, err := h.InspectSource(root)
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != "marketplace" || in.Market == nil || in.Preview != nil {
		t.Fatalf("应识别为 marketplace: %+v", in)
	}
	mk := in.Market
	if mk.Name != "team-tools" || mk.Owner != "Example Team" || mk.Description != "Plugins for the team" || mk.Token == "" {
		t.Fatalf("概况: %+v", mk)
	}
	byName := map[string]MarketPlugin{}
	for _, p := range mk.Plugins {
		if _, dup := byName[p.Name]; dup {
			t.Errorf("重名条目只应保留第一个: %s", p.Name)
		}
		byName[p.Name] = p
	}
	if _, ok := byName["bad name!"]; ok {
		t.Error("名字不合法的条目应被丢弃")
	}
	if len(mk.Plugins) != 16 {
		t.Errorf("条目数: %d", len(mk.Plugins))
	}

	supported := []string{"formatter", "bare", "native", "remote-gh", "mono", "pinned"}
	for _, n := range supported {
		if p := byName[n]; !p.Supported || p.Reason != "" {
			t.Errorf("%s 应可选: %+v", n, p)
		}
	}
	// 每个不支持的条目都要写明原因，而不是悄悄消失
	for n, want := range map[string]string{
		"evil": "不能指到", "slash": "必须以", "missing": "不存在", "plain-http": "https", "ssh": "https",
		"bad-repo": "owner/repo", "bad-sha": "sha", "from-npm": "npm", "from-zip": "压缩包", "from-cmd": "命令",
	} {
		p := byName[n]
		if p.Supported || !strings.Contains(p.Reason, want) {
			t.Errorf("%s 应不可选，原因里要有 %q: %+v", n, want, p)
		}
	}
	// 来源要让用户在选之前就看得到
	if byName["formatter"].Where != "plugins/formatter" || byName["bare"].Where != "plugins/bare" || byName["remote-gh"].Where != "acme/remote-gh" || byName["mono"].Where != "acme/monorepo › tools/mono" {
		t.Errorf("Where: %+v %+v %+v %+v", byName["formatter"], byName["bare"], byName["remote-gh"], byName["mono"])
	}
}

func TestMarketplaceInstallRelative(t *testing.T) {
	root := t.TempDir()
	writeMarketplace(t, root)
	h, _, _, _ := newTestHost(t)
	in, _ := h.InspectSource(root)
	tok := in.Market.Token

	// 相对路径的 Claude Code 布局插件：条目名当没写名字的插件的名字；命令能用
	pv, err := h.PreviewMarketplacePlugin(tok, "formatter")
	if err != nil {
		t.Fatal(err)
	}
	if pv.Plugin.ID != "formatter" || pv.Plugin.Format != "claude" || len(pv.Plugin.Commands) != 1 {
		t.Fatalf("%+v", pv.Plugin)
	}
	if _, err := h.Install(pv.Token, nil); err != nil {
		t.Fatal(err)
	}
	if text, ok, _ := h.ExpandCommand("fmt", "main.go"); !ok || text != "Format main.go" {
		t.Fatalf("命令: %q %v", text, ok)
	}

	// pluginRoot 下的裸名
	pv, err = h.PreviewMarketplacePlugin(tok, "bare")
	if err != nil || pv.Plugin.ID != "bare" || len(pv.Plugin.Skills) != 1 {
		t.Fatalf("裸名: %+v %v", pv.Plugin, err)
	}
	h.CancelPreview(pv.Token)
	// 原生插件
	pv, err = h.PreviewMarketplacePlugin(tok, "native")
	if err != nil || pv.Plugin.ID != "native" || pv.Plugin.Format != "" {
		t.Fatalf("原生: %+v %v", pv.Plugin, err)
	}
	h.CancelPreview(pv.Token)

	// 不能装的、不存在的、失效的 token
	if _, err := h.PreviewMarketplacePlugin(tok, "from-npm"); err == nil || !strings.Contains(err.Error(), "npm") {
		t.Errorf("不支持的来源应说明原因: %v", err)
	}
	if _, err := h.PreviewMarketplacePlugin(tok, "evil"); err == nil {
		t.Error("路径逃逸的条目不能装")
	}
	if _, err := h.PreviewMarketplacePlugin(tok, "nope"); err == nil {
		t.Error("没有这个条目")
	}
	h.CancelMarketplace(tok)
	if _, err := h.PreviewMarketplacePlugin(tok, "formatter"); err == nil || !strings.Contains(err.Error(), "失效") {
		t.Errorf("取消之后 token 应失效: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("本地文件夹形式的 marketplace 取消时不能被删掉")
	}
}

func TestInspectSourcePlainPluginAndErrors(t *testing.T) {
	h, _, _, _ := newTestHost(t)
	dir := t.TempDir()
	writeDeployTools(t, dir)
	in, err := h.InspectSource(dir)
	if err != nil || in.Kind != "plugin" || in.Preview == nil || in.Preview.Plugin.ID != "deploy-tools" {
		t.Fatalf("不是 marketplace 的目录应当作插件预览: %+v %v", in, err)
	}
	h.CancelPreview(in.Preview.Token)
	if _, err := h.InspectSource(filepath.Join(dir, "nope")); err == nil {
		t.Error("不存在的来源应报错")
	}
	// 坏的 marketplace 文件
	for name, body := range map[string]string{
		"not json":    `{`,
		"bad name":    `{"name":"bad name","owner":{"name":"x"},"plugins":[{"name":"a","source":"./a"}]}`,
		"no plugins":  `{"name":"ok","owner":{"name":"x"},"plugins":[]}`,
		"root escape": `{"name":"ok","owner":{"name":"x"},"metadata":{"pluginRoot":"../x"},"plugins":[{"name":"a","source":"a"}]}`,
		"all invalid": `{"name":"ok","owner":{"name":"x"},"plugins":[{"name":"!!","source":"./a"}]}`,
	} {
		d := t.TempDir()
		writeFile(t, filepath.Join(d, ".claude-plugin", "marketplace.json"), body)
		if _, err := h.InspectSource(d); err == nil {
			t.Errorf("%s：应报错", name)
		}
	}
}

// 假 git：只认我们用到的两条命令，让远程来源的整条流程（克隆 → 取子目录 → sha 校验）能在没有网络时测试。
func installFakeGit(t *testing.T, sha string) (logFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("假 git 是 shell 脚本")
	}
	bin := t.TempDir()
	logFile = filepath.Join(t.TempDir(), "git.log")
	script := `#!/bin/sh
echo "$@" >> "` + logFile + `"
case "$*" in
  *clone*)
    for last; do :; done   # 最后一个参数是目标目录
    mkdir -p "$last/tools/mono/skills/mono-skill" "$last/skills/root-skill"
    printf -- '---\nname: root-skill\n---\nbody' > "$last/skills/root-skill/SKILL.md"
    printf -- '---\nname: mono-skill\n---\nbody' > "$last/tools/mono/skills/mono-skill/SKILL.md"
    ;;
  *rev-parse*) echo "` + sha + `" ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

func TestMarketplaceRemoteSources(t *testing.T) {
	const pinned = "0123456789abcdef0123456789abcdef01234567"
	logFile := installFakeGit(t, pinned)
	root := t.TempDir()
	writeMarketplace(t, root)
	h, _, _, _ := newTestHost(t)
	h.opts.LookPath = func(string) (string, error) { return "git", nil }
	in, err := h.InspectSource(root)
	if err != nil {
		t.Fatal(err)
	}
	tok := in.Market.Token

	// github：地址由 owner/repo 拼成 https，分支用条目里的 ref；条目名当插件名
	pv, err := h.PreviewMarketplacePlugin(tok, "remote-gh")
	if err != nil {
		t.Fatal(err)
	}
	if pv.Plugin.ID != "remote-gh" || len(pv.Plugin.Skills) != 1 {
		t.Fatalf("github: %+v", pv.Plugin)
	}
	h.CancelPreview(pv.Token)
	// git-subdir：只取子目录里的插件，owner/repo 简写
	pv, err = h.PreviewMarketplacePlugin(tok, "mono")
	if err != nil || pv.Plugin.ID != "mono" || pv.Plugin.Skills[0] != "mono-skill" {
		t.Fatalf("git-subdir: %+v %v", pv.Plugin, err)
	}
	h.CancelPreview(pv.Token)
	// 固定了 sha 且克隆下来的提交一致：通过
	pv, err = h.PreviewMarketplacePlugin(tok, "pinned")
	if err != nil || pv.Plugin.ID != "pinned" {
		t.Fatalf("sha 一致: %+v %v", pv.Plugin, err)
	}
	h.CancelPreview(pv.Token)

	log, _ := os.ReadFile(logFile)
	for _, want := range []string{"https://github.com/acme/remote-gh", "--branch v2.0.0", "https://github.com/acme/monorepo", "https://git.example.com/acme/pinned.git", "rev-parse HEAD", "protocol.allow=never"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("git 调用里应有 %q:\n%s", want, log)
		}
	}
}

func TestMarketplaceShaMismatchRefused(t *testing.T) {
	installFakeGit(t, "ffffffffffffffffffffffffffffffffffffffff") // 克隆下来的提交与清单里固定的不一致
	root := t.TempDir()
	writeMarketplace(t, root)
	h, _, _, _ := newTestHost(t)
	h.opts.LookPath = func(string) (string, error) { return "git", nil }
	in, _ := h.InspectSource(root)
	_, err := h.PreviewMarketplacePlugin(in.Market.Token, "pinned")
	if err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("sha 对不上必须拒绝，而不是悄悄装另一个版本: %v", err)
	}
	// 拒绝之后不能留下半成品
	if entries, _ := os.ReadDir(filepath.Join(h.opts.Dir, ".staging")); len(entries) != 0 {
		t.Errorf("失败后不应残留临时目录: %v", entries)
	}
}

func TestInspectGitMarketplaceAndPlainRepo(t *testing.T) {
	installFakeGit(t, "")
	h, _, _, _ := newTestHost(t)
	h.opts.LookPath = func(string) (string, error) { return "git", nil }
	// 这个“仓库”（假 git 的克隆结果）里没有 marketplace.json，只有 skills/：当作一个插件，名字取仓库名
	in, err := h.InspectSource("https://github.com/acme/my-skills.git#main")
	if err != nil || in.Kind != "plugin" || in.Preview.Plugin.ID != "my-skills" {
		t.Fatalf("%+v %v", in, err)
	}
	h.CancelPreview(in.Preview.Token)
	// 没有 git：给出明确的提示
	h.opts.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if _, err := h.InspectSource("https://github.com/acme/x"); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("没有 git 应提示: %v", err)
	}
	// 地址不合法
	if _, err := h.InspectSource("https://user:pw@github.com/acme/x"); err == nil {
		t.Fatal("带用户名密码的地址应被拒绝")
	}
}
