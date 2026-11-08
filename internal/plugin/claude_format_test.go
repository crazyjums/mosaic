package plugin

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/mcp"
)

// writeDeployTools 写一个和 Claude Code 官方插件参考里的示例同样结构的插件：
// 清单在 .claude-plugin/，其余组件放在插件根目录的约定位置。
func writeDeployTools(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{
  "name": "deploy-tools",
  "displayName": "Deploy Tools",
  "version": "1.2.0",
  "description": "Deployment commands, a review agent, and a status monitor",
  "author": {"name": "Example Team"},
  "unknown_future_field": true,
  "userConfig": {
    "api_token": {"type": "string", "title": "API token", "description": "Token for the deployment API", "sensitive": true},
    "Region": {"type": "string", "title": "Region", "description": "Where to deploy", "options": ["eu", "us"], "default": "eu"},
    "dry_run": {"type": "boolean", "title": "Dry run", "description": "Do not apply", "default": true}
  }
}`)
	writeFile(t, filepath.Join(dir, "skills", "deploy", "SKILL.md"), "---\nname: deploy\ndescription: Deploy the app\n---\nSteps to deploy.")
	writeFile(t, filepath.Join(dir, "commands", "status.md"), "---\ndescription: Show the current deployment status\nargument-hint: [env]\nallowed-tools: Bash(git:*), Read\n---\nShow the deployment status of $ARGUMENTS.")
	writeFile(t, filepath.Join(dir, "commands", "rollback.md"), "---\ndescription: Roll back\n---\nRoll back $1 to version $2 (all args: $ARGUMENTS). Cost: $10.")
	writeFile(t, filepath.Join(dir, "commands", "diffstat.md"), "---\ndescription: Summarise changes\n---\nChanges: !`git diff --stat`\nSummarise them.")
	writeFile(t, filepath.Join(dir, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews deploys\n---\nYou review deploys.")
	writeFile(t, filepath.Join(dir, "hooks", "hooks.json"), `{"hooks":{"PostToolUse":[{"matcher":"Write|Edit","hooks":[{"type":"command","command":"\"${CLAUDE_PLUGIN_ROOT}\"/scripts/format.sh"}]}]}}`)
	writeFile(t, filepath.Join(dir, "scripts", "format.sh"), "#!/bin/sh\necho formatted\n")
	writeFile(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers":{
  "deploy-api": {"command": "node", "args": ["${CLAUDE_PLUGIN_ROOT}/server.js", "--region", "${user_config.Region}"], "env": {"TOKEN": "${user_config.api_token}", "DATA": "${CLAUDE_PLUGIN_DATA}"}},
  "remote": {"type": "http", "url": "https://mcp.example.com/mcp", "headers": {"Authorization": "Bearer ${user_config.api_token}"}},
  "live": {"type": "sse", "url": "https://mcp.example.com/sse"},
  "proj": {"command": "node", "args": ["${CLAUDE_PROJECT_DIR}/x.js"]}
}}`)
	writeFile(t, filepath.Join(dir, "server.js"), "// server")
	writeFile(t, filepath.Join(dir, "output-styles", "terse.md"), "terse")
	writeFile(t, filepath.Join(dir, "monitors", "monitors.json"), "[]")
	writeFile(t, filepath.Join(dir, ".lsp.json"), "{}")
}

func TestClaudeCodePluginImport(t *testing.T) {
	dir := t.TempDir()
	writeDeployTools(t, dir)
	m, err := ParseManifest(dir, "dev")
	if err != nil {
		t.Fatalf("Claude Code 布局应能导入: %v", err)
	}
	if m.ID != "deploy-tools" || m.Name != "Deploy Tools" || m.Version != "1.2.0" || m.Format != "claude" {
		t.Fatalf("基本信息: %+v", m)
	}

	// 技能：默认的 skills/<名>/SKILL.md
	if len(m.Skills) != 1 || m.Skills[0].Path != "skills/deploy" {
		t.Fatalf("技能: %+v", m.Skills)
	}
	// 命令：commands/ 下扁平的 .md，名字取文件名，描述取 frontmatter
	got := map[string]string{}
	for _, c := range m.Commands {
		got[c.Name] = c.Description
		if !c.claude {
			t.Errorf("导入的命令应按 Claude Code 写法展开: %s", c.Name)
		}
	}
	if len(got) != 3 || got["status"] != "Show the current deployment status" {
		t.Fatalf("命令: %v", got)
	}

	// 配置：userConfig 映射；sensitive → secret，boolean → bool，options → select，键转成小写
	cfg := map[string]ConfigField{}
	for _, c := range m.Config {
		cfg[c.Key] = c
	}
	if cfg["api_token"].Type != TypeSecret || cfg["api_token"].Default != "" {
		t.Errorf("sensitive 应变成 secret: %+v", cfg["api_token"])
	}
	if cfg["region"].Type != TypeSelect || len(cfg["region"].Options) != 2 || cfg["region"].Default != "eu" {
		t.Errorf("options 应变成 select（键转小写）: %+v", cfg["region"])
	}
	if cfg["dry_run"].Type != TypeBool || cfg["dry_run"].Default != "true" {
		t.Errorf("boolean: %+v", cfg["dry_run"])
	}

	// MCP：变量翻译成 Mosaic 的写法；风险档位一律是 exec；sse 和用了 ${CLAUDE_PROJECT_DIR} 的被跳过并告知
	servers := map[string]MCPSpec{}
	for _, s := range m.MCP {
		servers[s.Name] = s
		if s.Risk != "exec" {
			t.Errorf("导入的 MCP 服务风险档位必须是 exec: %s %q", s.Name, s.Risk)
		}
	}
	api := servers["deploy-api"]
	if api.Command != "node" || api.Args[0] != "${PLUGIN_DIR}/server.js" || api.Args[2] != "${config.region}" || api.Env["TOKEN"] != "${config.api_token}" || api.Env["DATA"] != "${DATA_DIR}" {
		t.Errorf("变量翻译: %+v", api)
	}
	if r := servers["remote"]; r.Transport != mcp.TransportHTTP || r.Headers["Authorization"] != "Bearer ${config.api_token}" {
		t.Errorf("http 服务: %+v", r)
	}
	if _, ok := servers["live"]; ok {
		t.Error("sse 传输暂不支持，应被跳过")
	}
	if _, ok := servers["proj"]; ok {
		t.Error("用了 ${CLAUDE_PROJECT_DIR} 的服务应被跳过")
	}
	if len(m.Requires.Commands) != 1 || m.Requires.Commands[0] != "node" {
		t.Errorf("应提前检测 node: %v", m.Requires.Commands)
	}

	// 子 agent 现在会被导入（启用时交给装配层解析登记），不再是“不会生效”
	if len(m.Agents) != 1 || m.Agents[0] != "agents/reviewer.md" {
		t.Fatalf("子 agent 文件: %v", m.Agents)
	}
	if strings.Contains(strings.Join(m.Ignored, "\n"), "子 agent") {
		t.Fatalf("子 agent 不应再列为不会生效: %v", m.Ignored)
	}

	// 不支持的组件必须列出来，不能悄悄忽略
	ig := strings.Join(m.Ignored, "\n")
	for _, want := range []string{"钩子", "语言服务器", "输出风格", "后台监视器", "live", "proj", "!`"} {
		if !strings.Contains(ig, want) {
			t.Errorf("忽略清单里应有 %q:\n%s", want, ig)
		}
	}
}

// 导入的插件走完整的 预览 → 安装 → 命令展开 流程；已安装的插件每次加载都要重新推出同一个 id。
func TestClaudeCodePluginInstallAndCommands(t *testing.T) {
	src := t.TempDir()
	writeDeployTools(t, src)
	h, fake, _, _ := newTestHost(t)

	pv, err := h.Preview(src)
	if err != nil {
		t.Fatalf("预览: %v", err)
	}
	if pv.Plugin.Format != "claude" || len(pv.Plugin.Ignored) == 0 || pv.Plugin.ID != "deploy-tools" {
		t.Fatalf("预览应带格式和被忽略的组件: %+v", pv.Plugin)
	}
	info, err := h.Install(pv.Token, nil)
	if err != nil {
		t.Fatalf("安装: %v", err)
	}
	if info.ID != "deploy-tools" || !info.Enabled || info.Error != "" {
		t.Fatalf("安装结果: %+v", info)
	}

	// 重新扫描（模拟重启应用）：目录名就是 id，导入器要得到同一个 id，不能报“目录名与 id 不一致”
	h2, err := NewHost(h.opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := h2.Get("deploy-tools"); err != nil || got.Error != "" {
		t.Fatalf("重启后加载: %+v %v", got, err)
	}

	// 命令：$ARGUMENTS 与 $1…$9
	text, ok, err := h2.ExpandCommand("status", "prod eu")
	if err != nil || !ok || text != "Show the deployment status of prod eu." {
		t.Fatalf("$ARGUMENTS: %q %v %v", text, ok, err)
	}
	text, _, _ = h2.ExpandCommand("rollback", `web "release 41"`)
	if text != "Roll back web to version release 41 (all args: web \"release 41\"). Cost: $10." {
		t.Fatalf("位置参数（引号内不拆，$10 保持原样）: %q", text)
	}
	text, _, _ = h2.ExpandCommand("rollback", "")
	if !strings.HasPrefix(text, "Roll back  to version  (all args: )") {
		t.Fatalf("没给参数时占位符为空: %q", text)
	}

	// 诊断：技能和命令就绪；MCP 服务因缺少 node 而需要配置
	d, err := h2.Diagnostics("deploy-tools")
	if err != nil || len(d.Commands) != 3 {
		t.Fatalf("诊断: %+v %v", d, err)
	}
	_ = fake
	_ = context.Background()
}

func TestClaudeCodeLayoutVariants(t *testing.T) {
	t.Run("没有清单：按约定目录找，名字来自来源", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "skills", "greet", "SKILL.md"), "---\nname: greet\n---\nhi")
		writeFile(t, filepath.Join(dir, "commands", "hello.md"), "Say hello to $ARGUMENTS")
		if _, err := ParseManifest(dir, "dev"); err == nil {
			t.Fatal("没有名字又没有清单，应报错而不是编一个名字")
		}
		m, err := ParseManifestHint(dir, "dev", "My_Cool Plugin")
		if err != nil || m.ID != "my-cool-plugin" || len(m.Skills) != 1 || len(m.Commands) != 1 {
			t.Fatalf("%+v %v", m, err)
		}
		// 同一个提示永远得到同一个 id
		m2, _ := ParseManifestHint(dir, "dev", "My_Cool Plugin")
		if m2.ID != m.ID {
			t.Fatal("id 必须稳定")
		}
	})
	t.Run("根目录只有 SKILL.md：整个插件就是一个技能", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: solo\n---\nbody")
		m, err := ParseManifestHint(dir, "dev", "solo-skill")
		if err != nil || len(m.Skills) != 1 || m.Skills[0].Path != "." {
			t.Fatalf("%+v %v", m, err)
		}
	})
	t.Run("清单的 commands 替换默认目录；skills 追加", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"cmdmap","commands":{"about":{"source":"./docs/about.md","description":"关于"},"inline":{"content":"inline body"}},"skills":["./extra"]}`)
		writeFile(t, filepath.Join(dir, "docs", "about.md"), "About this plugin")
		writeFile(t, filepath.Join(dir, "commands", "ignored.md"), "default dir is replaced")
		writeFile(t, filepath.Join(dir, "skills", "a", "SKILL.md"), "a")
		writeFile(t, filepath.Join(dir, "extra", "b", "SKILL.md"), "b")
		m, err := ParseManifest(dir, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Commands) != 1 || m.Commands[0].Name != "about" || m.Commands[0].Description != "关于" {
			t.Fatalf("命令映射: %+v", m.Commands)
		}
		if len(m.Skills) != 2 {
			t.Fatalf("默认 skills/ 照常扫描，清单里的追加: %+v", m.Skills)
		}
		if !strings.Contains(strings.Join(m.Ignored, "\n"), "inline") {
			t.Errorf("内联 content 的命令应被告知不支持: %v", m.Ignored)
		}
	})
	t.Run("只有不支持的组件：报错并说明有什么", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"hooks-only"}`)
		writeFile(t, filepath.Join(dir, "hooks", "hooks.json"), `{"hooks":{}}`)
		writeFile(t, filepath.Join(dir, ".lsp.json"), `{}`)
		_, err := ParseManifest(dir, "dev")
		if err == nil || !strings.Contains(err.Error(), "没有 Mosaic 能用的内容") || !strings.Contains(err.Error(), "钩子") || !strings.Contains(err.Error(), "语言服务器") {
			t.Fatalf("应说明为什么装不了: %v", err)
		}
	})
	t.Run("原生 plugin.json 优先于 .claude-plugin", func(t *testing.T) {
		dir := t.TempDir()
		writePlugin(t, dir, baseManifest("native"))
		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"other"}`)
		m, err := ParseManifest(dir, "dev")
		if err != nil || m.ID != "native" || m.Format != "" {
			t.Fatalf("%+v %v", m, err)
		}
	})
	t.Run("路径不能逃出插件目录", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"escape","commands":["../outside.md"],"skills":["../outside"]}`)
		writeFile(t, filepath.Join(dir, "skills", "ok", "SKILL.md"), "ok")
		m, err := ParseManifest(dir, "dev")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range m.Commands {
			if strings.Contains(c.Path, "..") {
				t.Fatalf("命令路径逃出了插件目录: %+v", c)
			}
		}
		if len(m.Skills) != 1 {
			t.Fatalf("只应有目录内的技能: %+v", m.Skills)
		}
	})
	t.Run("名字太长或带特殊字符：id 合法且稳定", func(t *testing.T) {
		for in, want := range map[string]string{
			"deploy-tools":                         "deploy-tools",
			"My Plugin!!":                          "my-plugin",
			"a-very-long-plugin-name-that-goes-on": "a-very-long-plugin-name",
			"__x__":                                "x",
			"中文":                                   "",
		} {
			if got := sanitizeID(in); got != want || (got != "" && !idRe.MatchString(got)) {
				t.Errorf("sanitizeID(%q) = %q, want %q", in, got, want)
			}
		}
	})
}

func TestClaudeCodeZipWithTopFolder(t *testing.T) {
	src := t.TempDir()
	writeDeployTools(t, src)
	zipPath := filepath.Join(t.TempDir(), "deploy-tools-main.zip")
	makeZip(t, src, zipPath, "deploy-tools-main/", nil)
	h, _, _, _ := newTestHost(t)
	pv, err := h.Preview(zipPath)
	if err != nil {
		t.Fatalf("GitHub 风格的压缩包（外面套一层目录）应能识别: %v", err)
	}
	if pv.Plugin.ID != "deploy-tools" || pv.Plugin.Format != "claude" {
		t.Fatalf("%+v", pv.Plugin)
	}
	if _, err := os.Stat(filepath.Join(h.opts.Dir, ".staging")); err != nil {
		t.Log("staging 目录状态:", err)
	}
}

func TestRenderClaudeTemplate(t *testing.T) {
	cases := []struct{ body, args, want string }{
		{"Do $ARGUMENTS now", "a b", "Do a b now"},
		{"first=$1 second=$2", "x y z", "first=x second=y"},
		{"first=$1", `"two words" tail`, "first=two words"},
		{"price $10 and $1", "9", "price $10 and 9"},
		{"no placeholders", "extra", "no placeholders\n\nextra"},
		{"no placeholders", "", "no placeholders"},
		{"only $3", "a", "only "},
	}
	for _, c := range cases {
		if got := (commandTemplate{body: c.body}).renderClaude(c.args); got != c.want {
			t.Errorf("renderClaude(%q, %q) = %q, want %q", c.body, c.args, got, c.want)
		}
	}
	// 原生模板里的 $1 就是字面的 $1，不受影响
	if got := (commandTemplate{body: "echo $1 {{args}}"}).render("x"); got != "echo $1 x" {
		t.Errorf("原生模板不应展开 $1: %q", got)
	}
}

// 评审发现：用户输入里的 $5、$ARGUMENTS 不能被当成占位符再展开一遍。
func TestRenderClaudeKeepsArgumentsLiteral(t *testing.T) {
	cases := []struct{ body, args, want string }{
		{"Explain $ARGUMENTS", "the price is $5", "Explain the price is $5"},
		{"Explain $ARGUMENTS", "use $ARGUMENTS here", "Explain use $ARGUMENTS here"},
		{"a=$1 all=$ARGUMENTS", "$2 x", "a=$2 all=$2 x"},
		{"first=$1 second=$2", "$1 $2", "first=$1 second=$2"},
	}
	for _, c := range cases {
		if got := (commandTemplate{body: c.body}).renderClaude(c.args); got != c.want {
			t.Errorf("renderClaude(%q, %q) = %q, want %q", c.body, c.args, got, c.want)
		}
	}
}

// 评审发现：归一化后撞键（Region / region）不能让整个插件被“重复 key”拒绝。
func TestClaudeUserConfigKeysStayUnique(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"keys","userConfig":{
  "Region": {"type":"string","title":"Region A","description":"d"},
  "region": {"type":"string","title":"Region B","description":"d"},
  "region-": {"type":"string","title":"Region C","description":"d"},
  "a_very_long_key_name_that_exceeds_the_limit_1": {"type":"string","title":"L1","description":"d"},
  "a_very_long_key_name_that_exceeds_the_limit_2": {"type":"string","title":"L2","description":"d"}
}}`)
	writeFile(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers":{"s":{"command":"node","args":["${user_config.region}","${user_config.Region}"]}}}`)
	m, err := ParseManifest(dir, "dev")
	if err != nil {
		t.Fatalf("撞键不应让整个插件装不上: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range m.Config {
		if seen[c.Key] {
			t.Fatalf("重复的键 %q: %+v", c.Key, m.Config)
		}
		seen[c.Key] = true
		if len(c.Key) > 32 {
			t.Fatalf("键太长: %q", c.Key)
		}
	}
	if len(m.Config) != 5 {
		t.Fatalf("5 个配置项都应保留: %+v", m.Config)
	}
	// 引用要翻译成各自的键，且两个引用指向不同的键
	a := m.MCP[0].Args
	if a[0] == a[1] || !strings.HasPrefix(a[0], "${config.") {
		t.Fatalf("两个不同的 userConfig 引用必须映射到不同的键: %v", a)
	}
	// 同一份输入永远得到同一个映射
	m2, _ := ParseManifest(dir, "dev")
	if m2.MCP[0].Args[0] != a[0] || m2.MCP[0].Args[1] != a[1] {
		t.Fatal("映射必须稳定")
	}
}

// fakeAgents 记录 Host 推给装配层的插件子 agent。
type fakeAgents struct {
	got map[string][]string
	dir map[string]string
}

func (f *fakeAgents) ApplyPluginAgents(id, dir string, rels []string) {
	if f.got == nil {
		f.got, f.dir = map[string][]string{}, map[string]string{}
	}
	f.got[id], f.dir[id] = rels, dir
}

func (f *fakeAgents) DescribePluginAgents(id, dir string, rels []string) []AgentView {
	var out []AgentView
	for _, r := range rels {
		out = append(out, AgentView{Name: id + ":" + r})
	}
	return out
}

func TestClaudePluginAgents(t *testing.T) {
	t.Run("默认扫描 agents/（递归），只有子 agent 的插件也能装", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"agents-only"}`)
		writeFile(t, filepath.Join(dir, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: d\n---\nbody")
		writeFile(t, filepath.Join(dir, "agents", "review", "security.md"), "---\nname: security\ndescription: d\n---\nbody")
		writeFile(t, filepath.Join(dir, "agents", ".hidden", "x.md"), "x")
		writeFile(t, filepath.Join(dir, "agents", "notes.txt"), "x")
		m, err := ParseManifest(dir, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(m.Agents, []string{"agents/review/security.md", "agents/reviewer.md"}) {
			t.Fatalf("%v", m.Agents)
		}
	})
	t.Run("清单里的 agents 替换默认目录；路径不能逃出插件目录；只能是 .md", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"listed","agents":["./custom/a.md","../escape.md","./custom/b.txt"]}`)
		writeFile(t, filepath.Join(dir, "custom", "a.md"), "---\nname: a\ndescription: d\n---\nbody")
		writeFile(t, filepath.Join(dir, "custom", "b.txt"), "x")
		writeFile(t, filepath.Join(dir, "agents", "default.md"), "---\nname: default\ndescription: d\n---\nbody")
		m, err := ParseManifest(dir, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(m.Agents, []string{"custom/a.md"}) {
			t.Fatalf("清单里的 agents 替换默认目录: %v", m.Agents)
		}
		ig := strings.Join(m.Ignored, "\n")
		if !strings.Contains(ig, "escape.md") || !strings.Contains(ig, "b.txt") {
			t.Fatalf("逃逸和非 .md 要告知: %v", m.Ignored)
		}
	})
	t.Run("清单里写了空列表：不带子 agent，不扫描默认目录", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"no-agents","agents":[]}`)
		writeFile(t, filepath.Join(dir, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: d\n---\nbody")
		writeFile(t, filepath.Join(dir, "commands", "x.md"), "do x")
		m, err := ParseManifest(dir, "dev")
		if err != nil || len(m.Agents) != 0 {
			t.Fatalf("%v %v", m.Agents, err)
		}
	})
	t.Run("启用时交给装配层，停用、卸载时清空", func(t *testing.T) {
		src := t.TempDir()
		writeDeployTools(t, src)
		h, _, _, _ := newTestHost(t)
		fa := &fakeAgents{}
		h.opts.Agents = fa
		pv, err := h.Preview(src)
		if err != nil {
			t.Fatal(err)
		}
		if len(pv.Plugin.Agents) != 1 || pv.Plugin.Agents[0].Name != "deploy-tools:agents/reviewer.md" {
			t.Fatalf("安装确认里要列出子 agent: %+v", pv.Plugin.Agents)
		}
		if _, err := h.Install(pv.Token, nil); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(fa.got["deploy-tools"], []string{"agents/reviewer.md"}) || fa.dir["deploy-tools"] == "" {
			t.Fatalf("启用后应交出子 agent: %v %v", fa.got, fa.dir)
		}
		if _, err := h.SetEnabled("deploy-tools", false); err != nil {
			t.Fatal(err)
		}
		if len(fa.got["deploy-tools"]) != 0 {
			t.Fatalf("停用后应清空: %v", fa.got["deploy-tools"])
		}
		if _, err := h.SetEnabled("deploy-tools", true); err != nil {
			t.Fatal(err)
		}
		if err := h.Uninstall("deploy-tools", true); err != nil {
			t.Fatal(err)
		}
		if len(fa.got["deploy-tools"]) != 0 {
			t.Fatalf("卸载后应清空: %v", fa.got["deploy-tools"])
		}
	})
}
