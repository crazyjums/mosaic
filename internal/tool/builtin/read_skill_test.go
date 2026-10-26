package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/tool"
)

func TestReadSkillTool(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	dir := filepath.Join(home, ".claude/skills/report")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\ndescription: 做报告\n---\n先读 ref.md，再运行脚本"), 0o644)
	os.WriteFile(filepath.Join(dir, "ref.md"), []byte("参考"), 0o644)
	os.WriteFile(filepath.Join(dir, "scripts/run.sh"), []byte("echo hi"), 0o644)
	os.WriteFile(filepath.Join(home, "secret"), []byte("机密"), 0o644)

	rs := NewReadSkill(home, nil)
	run := func(in string) tool.Result {
		return rs.Execute(context.Background(), tool.Call{Input: json.RawMessage(in), Workspace: ws})
	}

	r := run(`{"name":"report"}`)
	if r.IsError || !strings.Contains(r.Output, "先读 ref.md") || !strings.Contains(r.Output, "- ref.md") || !strings.Contains(r.Output, "- scripts/run.sh") || !strings.Contains(r.Output, dir) {
		t.Fatalf("正文 + 文件列表 + 目录: %+v", r)
	}
	if strings.Contains(r.Output, "description:") {
		t.Fatalf("不应带 frontmatter: %q", r.Output)
	}
	if r := run(`{"name":"report","path":"ref.md"}`); r.IsError || r.Output != "参考" {
		t.Fatalf("读资源: %+v", r)
	}
	if r := run(`{"name":"report","path":"../../secret"}`); !r.IsError {
		t.Fatalf("不能读到技能文件夹外面: %+v", r)
	}
	if r := run(`{"name":"nope"}`); !r.IsError || !strings.Contains(r.Output, "report") {
		t.Fatalf("找不到时应列出可用技能: %+v", r)
	}
	if r := NewReadSkill("", nil).Execute(context.Background(), tool.Call{Input: json.RawMessage(`{"name":"x"}`), Workspace: ws}); !r.IsError || !strings.Contains(r.Output, "没有任何可用") {
		t.Fatalf("没有技能: %+v", r)
	}
}
