package permission

import (
	"encoding/json"
	"testing"
)

func TestWildcard(t *testing.T) {
	cases := []struct {
		p, s string
		want bool
	}{
		{"go test*", "go test ./...", true},
		{"go test*", "go build", false},
		{"rm *", "rm -rf /", true},
		{"src/*", "src/a/b.go", true}, // * 可以跨越 /
		{"*.md", "docs/a.md", true},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"*", "", true},
		{"", "", true},
		{"中文*", "中文路径/x", true},
	}
	for _, c := range cases {
		if got := Wildcard(c.p, c.s); got != c.want {
			t.Errorf("Wildcard(%q, %q) = %v", c.p, c.s, got)
		}
	}
}

func TestSubject(t *testing.T) {
	if Subject(json.RawMessage(`{"command":" ls -la "}`)) != "ls -la" {
		t.Fatal("command")
	}
	if Subject(json.RawMessage(`{"path":"a.txt","content":"x"}`)) != "a.txt" {
		t.Fatal("path")
	}
	if Subject(json.RawMessage(`{"q":1}`)) != `{"q":1}` {
		t.Fatal("raw")
	}
}

func TestRulePolicy(t *testing.T) {
	inner := NewModePolicy(ModeDefault)
	p := NewRulePolicy(inner, []Rule{
		{Tool: "shell", Pattern: "rm *", Action: Deny},
		{Tool: "shell", Pattern: "go test*", Action: Allow},
		{Tool: "write_file", Pattern: "docs/*", Action: Allow},
		{Tool: "*", Pattern: "*.env", Action: Deny},
	})
	cmd := func(s string) json.RawMessage { b, _ := json.Marshal(map[string]string{"command": s}); return b }
	path := func(s string) json.RawMessage { b, _ := json.Marshal(map[string]string{"path": s}); return b }

	if p.Evaluate("shell", RiskExec, cmd("go test ./...")) != Allow {
		t.Error("allow 规则应放行")
	}
	if p.Evaluate("shell", RiskExec, cmd("rm -rf build")) != Deny {
		t.Error("deny 规则应拒绝")
	}
	if p.Evaluate("shell", RiskExec, cmd("make")) != Ask {
		t.Error("没命中规则应交给档位（default → 询问）")
	}
	if p.Evaluate("write_file", RiskWrite, path("docs/a.md")) != Allow || p.Evaluate("write_file", RiskWrite, path("src/a.go")) != Ask {
		t.Error("按路径匹配")
	}
	if p.Evaluate("read_file", RiskRead, path(".env")) != Deny {
		t.Error("* 工具规则对读操作也生效")
	}

	// 规则优先于档位：auto 下 deny 仍然生效，readonly 下 allow 仍然生效。
	_ = p.SetMode(ModeAuto)
	if p.Evaluate("shell", RiskExec, cmd("rm x")) != Deny {
		t.Error("deny 规则在 auto 档位下也应生效")
	}
	_ = p.SetMode(ModeReadOnly)
	if p.Evaluate("shell", RiskExec, cmd("go test")) != Allow {
		t.Error("allow 规则在 readonly 档位下也应生效")
	}
	if p.Mode() != ModeReadOnly {
		t.Error("档位应转发给内层策略")
	}

	if err := p.SetRules([]Rule{{Tool: "", Action: Allow}}); err == nil {
		t.Error("缺工具名应报错")
	}
	if err := p.SetRules([]Rule{{Tool: "x", Action: "maybe"}}); err == nil {
		t.Error("非法动作应报错")
	}
	if len(p.Rules()) != 4 {
		t.Error("非法规则不应覆盖原有规则")
	}
}
