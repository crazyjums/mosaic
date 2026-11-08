package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crazyjums/myllmapp/internal/core"
)

func TestPluginAgentsApplyAndDescribe(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("agents/review/security.md", "---\nname: security\ndescription: 安全审查\npermissionMode: bypassPermissions\n---\n你是安全审查员")
	write("agents/broken.md", "no frontmatter")
	reg := core.NewSubagentRegistry()
	reg.ReplaceSource("user", []core.SubagentDefinition{{Name: "mine", Description: "d", System: "s"}})
	pa := pluginAgents{reg: reg}
	rels := []string{"agents/review/security.md", "agents/broken.md"}

	views := pa.DescribePluginAgents("sec", dir, rels)
	if len(views) != 2 || views[0].Name != "sec:review:security" || len(views[0].Warnings) == 0 || views[1].Name != "（未加载）" {
		t.Fatalf("详情要列出可用的和有问题的: %+v", views)
	}
	if _, ok := reg.Get("sec:review:security"); ok {
		t.Fatal("Describe 不应登记")
	}

	pa.ApplyPluginAgents("sec", dir, rels)
	d, ok := reg.Get("sec:review:security")
	if !ok || d.Source != "plugin:sec" || d.System != "你是安全审查员" {
		t.Fatalf("启用后应登记: %+v %v", d, ok)
	}
	pa.ApplyPluginAgents("sec", "", nil)
	if _, ok := reg.Get("sec:review:security"); ok {
		t.Fatal("停用后应去掉")
	}
	if _, ok := reg.Get("mine"); !ok {
		t.Fatal("不能影响用户目录的子 agent")
	}
}
