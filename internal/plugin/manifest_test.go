package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestOK(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, baseManifest("demo"))
	m, err := ParseManifest(dir, "1.3.2")
	if err != nil || m.ID != "demo" || len(m.MCP) != 1 || m.ServerName(m.MCP[0]) != "demo" {
		t.Fatalf("%v %+v", err, m)
	}
	if got := m.ServerName(MCPSpec{Name: "docs"}); got != "demo-docs" {
		t.Fatalf("服务名: %q", got)
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := map[string]struct {
		edit func(m map[string]any)
		want string
	}{
		"id 大写":         {func(m map[string]any) { m["id"] = "Demo" }, "id"},
		"id 太长":         {func(m map[string]any) { m["id"] = strings.Repeat("a", 25) }, "id"},
		"id 带斜杠":        {func(m map[string]any) { m["id"] = "a/b" }, "id"},
		"没有名字":          {func(m map[string]any) { m["name"] = " " }, "name"},
		"版本不合法":         {func(m map[string]any) { m["version"] = "latest" }, "version"},
		"hooks 不支持":     {func(m map[string]any) { m["hooks"] = []any{map[string]any{"point": "pre_tool_use"}} }, "钩子"},
		"subagents 不支持": {func(m map[string]any) { m["subagents"] = []any{1} }, "子 agent"},
		"技能路径逃逸":        {func(m map[string]any) { m["skills"] = []any{map[string]any{"path": "../x"}} }, "插件目录之外"},
		"技能绝对路径":        {func(m map[string]any) { m["skills"] = []any{map[string]any{"path": "/etc"}} }, "绝对路径"},
		"技能目录没有 SKILL":  {func(m map[string]any) { m["skills"] = []any{map[string]any{"path": "commands"}} }, "SKILL.md"},
		"命令名大写": {func(m map[string]any) {
			m["commands"] = []any{map[string]any{"name": "Sum", "path": "commands/sum.md"}}
		}, "命令名"},
		"命令重名": {func(m map[string]any) {
			m["commands"] = []any{map[string]any{"name": "sum", "path": "commands/sum.md"}, map[string]any{"name": "sum", "path": "commands/sum.md"}}
		}, "重复"},
		"命令文件不存在":   {func(m map[string]any) { m["commands"] = []any{map[string]any{"name": "x", "path": "commands/none.md"}} }, "模板文件"},
		"mcp 没有命令":  {func(m map[string]any) { m["mcp"] = []any{map[string]any{"name": "x"}} }, "command"},
		"mcp 风险不合法": {func(m map[string]any) { m["mcp"] = []any{map[string]any{"command": "x", "risk": "root"}} }, "risk"},
		"mcp 未知变量":  {func(m map[string]any) { m["mcp"] = []any{map[string]any{"command": "${HOME}/x"}} }, "未知的变量"},
		"mcp 引用没声明的配置": {func(m map[string]any) {
			m["mcp"] = []any{map[string]any{"command": "x", "args": []any{"${config.nope}"}}}
		}, "未知的变量"},
		"mcp 环境变量名不合法": {func(m map[string]any) {
			m["mcp"] = []any{map[string]any{"command": "x", "env": map[string]any{"A B": "1"}}}
		}, "环境变量名"},
		"mcp http 地址不合法": {func(m map[string]any) {
			m["mcp"] = []any{map[string]any{"transport": "http", "url": "ftp://x"}}
		}, "url"},
		"配置类型不支持": {func(m map[string]any) {
			m["config"] = []any{map[string]any{"key": "a", "label": "A", "type": "color"}}
		}, "类型"},
		"配置 key 重复": {func(m map[string]any) {
			m["config"] = []any{map[string]any{"key": "a", "label": "A", "type": "string"}, map[string]any{"key": "a", "label": "B", "type": "string"}}
		}, "重复"},
		"secret 不能有默认值": {func(m map[string]any) {
			m["config"] = []any{map[string]any{"key": "a", "label": "A", "type": "secret", "default": "x"}}
		}, "secret"},
		"select 没有选项": {func(m map[string]any) {
			m["config"] = []any{map[string]any{"key": "a", "label": "A", "type": "select"}}
		}, "options"},
		"状态命令引用未声明配置": {func(m map[string]any) { m["status"] = map[string]any{"command": "x ${config.zzz}"} }, "未知的变量"},
		"操作 id 重复": {func(m map[string]any) {
			m["actions"] = []any{map[string]any{"id": "a", "label": "A", "command": "x"}, map[string]any{"id": "a", "label": "B", "command": "y"}}
		}, "重复"},
		"依赖名不合法":  {func(m map[string]any) { m["requires"] = map[string]any{"commands": []any{"a b; rm"}} }, "requires"},
		"域名不合法":   {func(m map[string]any) { m["network"] = []any{"a b"} }, "network"},
		"最低版本不合法": {func(m map[string]any) { m["min_app_version"] = "新版" }, "min_app_version"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			m := baseManifest("demo")
			c.edit(m)
			writePlugin(t, dir, m)
			_, err := ParseManifest(dir, "1.3.2")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("应因 %q 被拒绝: %v", c.want, err)
			}
		})
	}
}

func TestParseManifestRejectsSymlinkEscapeAndBadFiles(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "SKILL.md"), "x")
	m := baseManifest("demo")
	writePlugin(t, dir, m)
	if err := os.Symlink(outside, filepath.Join(dir, "evil")); err != nil {
		t.Skip("不能创建符号链接")
	}
	m["skills"] = []any{map[string]any{"path": "evil"}}
	writePlugin(t, dir, m)
	if _, err := ParseManifest(dir, "1.3.2"); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("指到插件目录外的符号链接应被拒绝: %v", err)
	}

	if _, err := ParseManifest(t.TempDir(), "1.3.2"); err == nil {
		t.Fatal("没有清单应报错")
	}
	bad := t.TempDir()
	writeFile(t, filepath.Join(bad, FileName), "{不是 json")
	if _, err := ParseManifest(bad, "1.3.2"); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("坏 JSON: %v", err)
	}
	big := t.TempDir()
	writeFile(t, filepath.Join(big, FileName), strings.Repeat(" ", maxManifestBytes+10))
	if _, err := ParseManifest(big, "1.3.2"); err == nil || !strings.Contains(err.Error(), "太大") {
		t.Fatalf("超大清单: %v", err)
	}
}

func TestCompat(t *testing.T) {
	dir := t.TempDir()
	m := baseManifest("demo")
	m["min_app_version"] = "9.0.0"
	writePlugin(t, dir, m)
	_, err := ParseManifest(dir, "1.3.2")
	var inc *IncompatibleError
	if !errors.As(err, &inc) || !strings.Contains(err.Error(), "升级应用") {
		t.Fatalf("应用版本太低: %v", err)
	}
	if _, err := ParseManifest(dir, "dev"); err != nil {
		t.Fatalf("开发版不检查版本: %v", err)
	}
	m["min_app_version"] = "1.3.2"
	m["protocol"] = 99
	writePlugin(t, dir, m)
	if _, err := ParseManifest(dir, "1.3.2"); !errors.As(err, &inc) {
		t.Fatalf("协议不一致: %v", err)
	}
}

func TestSplitAndExpand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`lark-cli auth status --format json`, []string{"lark-cli", "auth", "status", "--format", "json"}},
		{`node "a b/c.js" 'x y' z`, []string{"node", "a b/c.js", "x y", "z"}},
		{`C:\tools\x.exe --p "C:\Program Files\a"`, []string{`C:\tools\x.exe`, "--p", `C:\Program Files\a`}},
		{`echo "say \"hi\""`, []string{"echo", `say "hi"`}},
		{`a ""`, []string{"a", ""}},
	}
	for _, c := range cases {
		got, err := splitCommand(c.in)
		if err != nil || strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%q → %q (%v)", c.in, got, err)
		}
	}
	for _, bad := range []string{"", "   ", `a "b`} {
		if _, err := splitCommand(bad); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
	// 变量的值里有空格、引号、分号，也只是同一个参数的一部分：不会产生新参数，也没有 shell 解释。
	got, err := expandArgs(`run --name ${config.name} ${PLUGIN_DIR}/x.js`, map[string]string{"config.name": `a b"; rm -rf /`, "PLUGIN_DIR": "/p q"})
	if err != nil || len(got) != 4 || got[2] != `a b"; rm -rf /` || got[3] != "/p q/x.js" {
		t.Fatalf("先拆后展开: %q %v", got, err)
	}
	if _, err := expand("${NOPE}", map[string]string{}); err == nil {
		t.Fatal("未知变量应报错")
	}
}

func TestJSONPathAndTruthy(t *testing.T) {
	v, ok := jsonPath([]byte(`{"a":{"b":true},"s":"unauthenticated","n":0}`), "a.b")
	if !ok || !truthy(v) {
		t.Fatal("a.b")
	}
	for path, want := range map[string]bool{"s": false, "n": false} {
		v, ok := jsonPath([]byte(`{"s":"unauthenticated","n":0}`), path)
		if !ok || truthy(v) != want {
			t.Errorf("%s", path)
		}
	}
	if _, ok := jsonPath([]byte(`{"a":1}`), "a.b"); ok {
		t.Fatal("路径穿过非对象应取不到")
	}
	if _, ok := jsonPath([]byte(`not json`), "a"); ok {
		t.Fatal("非 JSON")
	}
}

func TestCommandTemplate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.md")
	writeFile(t, p, "\xef\xbb\xbf---\r\ndescription: \"总结\"\r\n---\r\n请总结：{{args}}\r\n")
	tpl, err := loadCommandTemplate(p)
	if err != nil || tpl.description != "总结" || tpl.render(" 这篇文档 ") != "请总结：这篇文档" {
		t.Fatalf("%v %+v", err, tpl)
	}
	writeFile(t, p, "没有占位符")
	tpl, _ = loadCommandTemplate(p)
	if tpl.render("") != "没有占位符" || tpl.render("补充") != "没有占位符\n\n补充" {
		t.Fatalf("没有 {{args}} 时接在后面: %q", tpl.render("补充"))
	}
	writeFile(t, p, "---\ndescription: x\n---\n")
	if _, err := loadCommandTemplate(p); err == nil {
		t.Fatal("空模板应报错")
	}
}
