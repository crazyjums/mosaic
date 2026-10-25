package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFrontmatter(t *testing.T) {
	cases := []struct{ in, name, desc, body string }{
		{"---\nname: a\ndescription: 做报告\n---\n正文", "a", "做报告", "正文"},
		{"---\r\nname: \"b\"\r\ndescription: 'x: y'\r\n---\r\n正文", "b", "x: y", "正文"},
		{"---\ndescription: >-\n  第一行\n  第二行\n---\nbody", "", "第一行 第二行", "body"},
		{"---\ndescription: |\n  行一\n  行二\n---\nbody", "", "行一\n行二", "body"},
		{"\xef\xbb\xbf---\nname: c\n---\nbody", "c", "", "body"},
		{"没有 frontmatter", "", "", "没有 frontmatter"},
		{"---\nname: 没有结束线\n正文", "", "", "---\nname: 没有结束线\n正文"},
	}
	for _, c := range cases {
		meta, body := splitFrontmatter(c.in)
		if meta["name"] != c.name || meta["description"] != c.desc || body != c.body {
			t.Errorf("%q → %v / %q", c.in, meta, body)
		}
	}
}

func TestDiscoverPriorityAndFormats(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	// 同名技能：工作区的 .myllmapp 优先于 .claude，再优先于用户目录。
	write(t, filepath.Join(ws, ".myllmapp/skills/report/SKILL.md"), "---\ndescription: 项目版\n---\n项目正文")
	write(t, filepath.Join(ws, ".claude/skills/report/SKILL.md"), "---\ndescription: claude 版\n---\nx")
	write(t, filepath.Join(home, ".claude/skills/report/SKILL.md"), "---\ndescription: 用户版\n---\nx")
	write(t, filepath.Join(home, ".claude/skills/review/SKILL.md"), "---\nname: code-review\ndescription: 审查代码\n---\nx")
	write(t, filepath.Join(home, ".myllmapp/skills/nodesc/SKILL.md"), "# 标题\n\n没有写简介时取这一段。")
	write(t, filepath.Join(home, ".claude/skills/.hidden/SKILL.md"), "x")
	write(t, filepath.Join(home, ".claude/skills/bad name/SKILL.md"), "x")
	write(t, filepath.Join(home, ".claude/skills/empty/README.md"), "没有 SKILL.md")

	got := Discover(ws, home)
	names := []string{}
	for _, s := range got {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "code-review,nodesc,report" {
		t.Fatalf("应只含合法技能且同名只取一个: %v", names)
	}
	for _, s := range got {
		switch s.Name {
		case "report":
			if s.Description != "项目版" || s.Source != SourceProject || s.Origin != ".myllmapp/skills" {
				t.Errorf("同名应取工作区的 .myllmapp: %+v", s)
			}
		case "nodesc":
			if s.Description != "没有写简介时取这一段。" || s.Source != SourceUser {
				t.Errorf("无简介时取正文第一段: %+v", s)
			}
		}
	}
	if s, ok := Find(ws, home, "/Report"); !ok || s.Name != "report" {
		t.Fatalf("Find 应忽略大小写和前导 /: %+v %v", s, ok)
	}
	if _, ok := Find(ws, home, "nope"); ok {
		t.Fatal("不存在的技能不应找到")
	}
}

func TestBodyAndResources(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/skills/pdf")
	write(t, filepath.Join(dir, "SKILL.md"), "---\nname: pdf\ndescription: d\n---\n步骤：先读 ref.md")
	write(t, filepath.Join(dir, "ref.md"), "参考资料")
	write(t, filepath.Join(dir, "scripts/run.py"), "print(1)")
	write(t, filepath.Join(dir, "bin.dat"), "a\x00b")
	write(t, filepath.Join(home, "secret.txt"), "机密")
	if err := os.Symlink(filepath.Join(home, "secret.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skip("不能创建符号链接:", err)
	}
	s, _ := Find("", home, "pdf")

	if b, err := Body(s); err != nil || b != "步骤：先读 ref.md" {
		t.Fatalf("正文应去掉 frontmatter: %q %v", b, err)
	}
	files := Files(s)
	if strings.Join(files, ",") != "bin.dat,link.txt,ref.md,scripts/run.py" {
		t.Fatalf("资源文件列表: %v", files)
	}
	if txt, err := ReadResource(s, "scripts/run.py"); err != nil || txt != "print(1)" {
		t.Fatalf("读资源: %q %v", txt, err)
	}
	for _, bad := range []string{"../secret.txt", "../../x", "link.txt", "/etc/passwd", "", "scripts"} {
		if _, err := ReadResource(s, bad); err == nil {
			t.Errorf("应拒绝 %q", bad)
		}
	}
	if _, err := ReadResource(s, "bin.dat"); err == nil {
		t.Error("二进制文件应拒绝")
	}
}

func TestPrompt(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	if Prompt(ws, home) != "" {
		t.Fatal("没有技能时不应改动系统提示词")
	}
	write(t, filepath.Join(ws, ".myllmapp/skills/a/SKILL.md"), "---\ndescription: 做 A\n---\nx")
	p := Prompt(ws, home)
	if !strings.Contains(p, "- a: 做 A") || !strings.Contains(p, "read_skill") || !strings.Contains(p, "/skill-name") {
		t.Fatalf("提示词: %q", p)
	}
	if strings.Contains(p, "x\n") && strings.Contains(p, "正文") {
		t.Fatal("提示词里不应带正文")
	}
}

func TestDiscoverIsBounded(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < maxSkills+20; i++ {
		write(t, filepath.Join(home, ".claude/skills", "s"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26)), "SKILL.md"), "x")
	}
	if got := Discover("", home); len(got) > maxSkills {
		t.Fatalf("应限制数量: %d", len(got))
	}
}
