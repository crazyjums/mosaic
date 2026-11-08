package agents

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseClaudeCodeAgent(t *testing.T) {
	d, err := Parse([]byte(`---
name: code-reviewer
description: Reviews code for quality and best practices
tools: Read, Glob, Grep, Bash(git diff *), WebFetch
model: sonnet
maxTurns: 8
permissionMode: bypassPermissions
---

You are a code reviewer.
Be concise.
`), "file-name")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "code-reviewer" || d.Description != "Reviews code for quality and best practices" || d.System != "You are a code reviewer.\nBe concise." {
		t.Fatalf("%+v", d)
	}
	if !slices.Equal(d.Tools, []string{"read_file", "find_files", "search_content", "shell"}) {
		t.Fatalf("工具名映射: %v", d.Tools)
	}
	if d.Model != "sonnet" || d.MaxSteps != 8 {
		t.Fatalf("model/maxTurns: %+v", d)
	}
	w := strings.Join(d.Warnings, "\n")
	if !strings.Contains(w, "WebFetch") || !strings.Contains(w, "permissionMode") {
		t.Fatalf("对不上的工具和 permissionMode 都要告知: %v", d.Warnings)
	}
}

func TestParseVariants(t *testing.T) {
	t.Run("YAML 列表、disallowedTools、MCP 工具名、Mosaic 自己的名字、inherit", func(t *testing.T) {
		d, err := Parse([]byte("---\nname: x\ndescription: d\ntools:\n  - Read\n  - mcp__fs__read\n  - git_log\ndisallowedTools: [Bash]\nmodel: inherit\n---\nbody"), "")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(d.Tools, []string{"read_file", "mcp__fs__read", "git_log"}) || !slices.Equal(d.DisallowedTools, []string{"shell"}) || d.Model != "" {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("列表项顶格写（和键对齐）也认", func(t *testing.T) {
		d, err := Parse([]byte("---\nname: x\ndescription: d\ntools:\n- Read\n- Grep\ndisallowedTools:\n- Bash\n---\nbody"), "")
		if err != nil || !slices.Equal(d.Tools, []string{"read_file", "search_content"}) || !slices.Equal(d.DisallowedTools, []string{"shell"}) {
			t.Fatalf("顶格列表不能丢: %+v %v", d, err)
		}
	})
	t.Run("写了 tools 却读不出任何项：不运行，不是放开全部", func(t *testing.T) {
		d, err := Parse([]byte("---\nname: x\ndescription: d\ntools:\n---\nbody"), "")
		if err != nil || !slices.Equal(d.Tools, []string{"__none__"}) || len(d.Warnings) == 0 {
			t.Fatalf("%+v %v", d, err)
		}
	})
	t.Run("名字取文件名", func(t *testing.T) {
		d, err := Parse([]byte("---\ndescription: d\n---\nbody"), "from-file")
		if err != nil || d.Name != "from-file" {
			t.Fatalf("%+v %v", d, err)
		}
	})
	t.Run("白名单一个都对不上：保留“有白名单”，运行时拒绝", func(t *testing.T) {
		d, err := Parse([]byte("---\nname: x\ndescription: d\ntools: WebSearch, WebFetch\n---\nbody"), "")
		if err != nil || len(d.Tools) == 0 {
			t.Fatalf("不能变成“没有白名单 = 全部工具”: %+v %v", d, err)
		}
	})
	for name, src := range map[string]string{
		"没有 frontmatter": "just text",
		"没结束":            "---\nname: x\ndescription: d\nbody",
		"没有描述":           "---\nname: x\n---\nbody",
		"正文空":            "---\nname: x\ndescription: d\n---\n",
		"名字不合法":          "---\nname: a b\ndescription: d\n---\nbody",
	} {
		if _, err := Parse([]byte(src), ""); err == nil {
			t.Errorf("%s：应报错", name)
		}
	}
	if _, err := Parse([]byte(strings.Repeat("x", maxFileBytes+1)), ""); err == nil {
		t.Error("太大的文件应报错")
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("reviewer.md", "---\nname: reviewer\ndescription: d\n---\nbody")
	write("review/security.md", "---\nname: security\ndescription: d\n---\nbody")
	write("broken.md", "no frontmatter")
	write(".hidden/x.md", "---\nname: hidden\ndescription: d\n---\nbody")
	write("notes.txt", "ignored")

	defs, problems := LoadDir(dir, "")
	var names []string
	for _, d := range defs {
		names = append(names, d.Name)
	}
	if !slices.Equal(names, []string{"security", "reviewer"}) && !slices.Equal(names, []string{"reviewer", "security"}) {
		t.Fatalf("names: %v", names)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "broken.md") {
		t.Fatalf("坏文件要报告、不影响其他: %v", problems)
	}

	// 插件：名字带上插件 id 和子目录
	defs, _ = LoadDir(dir, "my-plugin")
	names = names[:0]
	for _, d := range defs {
		names = append(names, d.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"my-plugin:review:security", "my-plugin:reviewer"}) {
		t.Fatalf("插件的名字: %v", names)
	}
	if defs, problems := LoadDir(filepath.Join(dir, "nope"), ""); len(defs) != 0 || len(problems) != 0 {
		t.Fatal("目录不存在不是错误")
	}
}
