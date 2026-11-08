package app

import (
	"encoding/json"
	"testing"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// 试调用绕开对话和权限弹窗，所以资格规则必须保守：只读、不敏感、无必填参数，缺一不可。
func TestTrialReason(t *testing.T) {
	obj := func(s string) json.RawMessage { return json.RawMessage(s) }
	cases := []struct {
		name string
		spec tool.Spec
		ok   bool
	}{
		{"只读、无参数", tool.Spec{Risk: permission.RiskRead, InputSchema: obj(`{"type":"object","properties":{}}`)}, true},
		{"只读、没有 schema", tool.Spec{Risk: permission.RiskRead}, true},
		{"只读、有可选参数", tool.Spec{Risk: permission.RiskRead, InputSchema: obj(`{"type":"object","properties":{"n":{"type":"number"}}}`)}, true},
		{"只读、required 为空数组", tool.Spec{Risk: permission.RiskRead, InputSchema: obj(`{"type":"object","required":[]}`)}, true},
		{"只读、有必填参数", tool.Spec{Risk: permission.RiskRead, InputSchema: obj(`{"type":"object","required":["path"]}`)}, false},
		{"只读但 schema 解析不了：按需要参数处理", tool.Spec{Risk: permission.RiskRead, InputSchema: obj(`{`)}, false},
		{"写入", tool.Spec{Risk: permission.RiskWrite, InputSchema: obj(`{}`)}, false},
		{"执行（用户没有信任只读声明时就是它）", tool.Spec{Risk: permission.RiskExec, InputSchema: obj(`{}`)}, false},
		{"风险为空", tool.Spec{InputSchema: obj(`{}`)}, false},
		{"敏感的只读工具", tool.Spec{Risk: permission.RiskRead, Sensitive: true, InputSchema: obj(`{}`)}, false},
	}
	for _, c := range cases {
		reason := trialReason(c.spec)
		if (reason == "") != c.ok {
			t.Errorf("%s: reason=%q, 期望可试调用=%v", c.name, reason, c.ok)
		}
		if !c.ok && reason == "" {
			t.Errorf("%s: 不能试调用时必须给出原因", c.name)
		}
	}
}

func TestFirstLineAndSort(t *testing.T) {
	if got := firstLine("  第一行\n第二行"); got != "第一行" {
		t.Errorf("firstLine: %q", got)
	}
	v := []PluginToolView{{Name: "b"}, {Name: "a"}, {Name: "c"}}
	sortToolViews(v)
	if v[0].Name != "a" || v[2].Name != "c" {
		t.Errorf("sort: %+v", v)
	}
}
