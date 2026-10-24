package provider

import (
	"context"
	"errors"
	"testing"
)

type stubProvider struct{ name string }

func (s stubProvider) Name() string { return s.name }
func (s stubProvider) Stream(context.Context, Request, func(Delta)) error {
	return nil
}

func TestCatalog(t *testing.T) {
	reg := NewRegistry()
	reg.Register("stub", func(cfg ProfileConfig) (Provider, error) {
		if cfg.APIKey == "" {
			return nil, errors.New("missing api key")
		}
		return stubProvider{name: cfg.Name}, nil
	})

	cat := NewCatalog(reg, []ProfileConfig{
		{Name: "ok", Type: "stub", APIKey: "k", Models: []string{"m1"}, DefaultModel: "m1"},
		{Name: "nokey", Type: "stub"},
		{Name: "bad", Type: "unknown"},
	})

	if p, _, err := cat.Get("ok"); err != nil || p.Name() != "ok" {
		t.Fatalf("ok: %v", err)
	}
	if _, _, err := cat.Get("nokey"); err == nil {
		t.Fatal("缺少密钥的配置档应不可用")
	}
	if _, _, err := cat.Get("missing"); err == nil {
		t.Fatal("不存在的配置档应报错")
	}

	infos := cat.Profiles()
	if len(infos) != 3 || !infos[0].Available || infos[1].Available || infos[2].Available {
		t.Fatalf("profiles: %+v", infos)
	}
	if !infos[0].HasAPIKey || infos[1].HasAPIKey {
		t.Fatal("HasAPIKey 应只反映是否配置，不泄露明文")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	reg := NewRegistry()
	reg.Register("x", nil)
	defer func() {
		if recover() == nil {
			t.Fatal("重复注册应 panic")
		}
	}()
	reg.Register("x", nil)
}

func TestMessageHelpers(t *testing.T) {
	m := Message{Role: RoleAssistant, Content: []Block{
		{Type: BlockThinking, Text: "hmm"},
		{Type: BlockText, Text: "a"},
		{Type: BlockToolUse, ToolCallID: "1", ToolName: "t"},
		{Type: BlockText, Text: "b"},
	}}
	if m.Text() != "ab" || len(m.ToolCalls()) != 1 {
		t.Fatalf("text=%q calls=%d", m.Text(), len(m.ToolCalls()))
	}
}
