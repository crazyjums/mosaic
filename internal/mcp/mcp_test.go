package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

type fakeClient struct{ called string }

func (f *fakeClient) ListTools(context.Context) ([]ToolInfo, error) {
	return []ToolInfo{{Name: "echo", Description: "echo back"}}, nil
}

func (f *fakeClient) CallTool(_ context.Context, name string, args json.RawMessage) (CallResult, error) {
	f.called = name
	return CallResult{Text: string(args)}, nil
}

func (f *fakeClient) Close() error { return nil }

// 证明扩展点可用：MCP 工具注册进普通的工具注册表，按普通工具执行。
func TestRegisterServerTools(t *testing.T) {
	reg := tool.NewRegistry()
	c := &fakeClient{}
	if err := RegisterServerTools(context.Background(), reg, "demo", c, ""); err != nil {
		t.Fatal(err)
	}
	tl, ok := reg.Get("mcp__demo__echo")
	if !ok {
		t.Fatal("工具应以带前缀的名字注册")
	}
	if tl.Spec().Risk != permission.RiskExec {
		t.Fatal("默认风险等级应为 exec（保守）")
	}
	r := tl.Execute(context.Background(), tool.Call{Input: json.RawMessage(`{"x":1}`)})
	if r.Output != `{"x":1}` || c.called != "echo" {
		t.Fatalf("应调用原始工具名: %+v %s", r, c.called)
	}
}

func TestToolAdapterSensitive(t *testing.T) {
	a := &ToolAdapter{Server: "s", Info: ToolInfo{Name: "t"}, Sensitive: true}
	if sp := a.Spec(); !sp.Sensitive || sp.Risk != permission.RiskExec {
		t.Fatalf("%+v", sp)
	}
	if (&ToolAdapter{Server: "s", Info: ToolInfo{Name: "t"}}).Spec().Sensitive {
		t.Fatal("默认不是私密工具")
	}
}
