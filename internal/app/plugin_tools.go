package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// 插件详情里的“试调用”：装好 MCP 插件之后，用户最想知道的是“它到底能不能用”。
//
// 试调用绕开对话（不经过模型、不弹权限窗），所以只对**最保守**的那一类工具开放，三个条件缺一不可：
//   - 风险档位是只读——也就是用户在安装确认里主动勾选过“信任它的只读声明”的（没勾选的一律是 exec，不能试调用）；
//   - 不涉及私密内容（Sensitive 的工具每次调用都要询问，不能绕过）；
//   - 不需要参数（试调用只发空参数，不让用户在这里构造任意输入）。
// 其余的工具照常在对话里用，走正常的权限确认。

// PluginToolView 是插件提供的一个工具，以及它能不能“试调用”。
type PluginToolView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Risk        string `json:"risk"`
	// Trial 为 true 表示可以试调用；否则 Reason 说明为什么不行。
	Trial  bool   `json:"trial"`
	Reason string `json:"reason,omitempty"`
}

// TryResult 是一次试调用的结果。
type TryResult struct {
	Output     string `json:"output"`
	IsError    bool   `json:"is_error"`
	DurationMs int64  `json:"duration_ms"`
}

const (
	tryTimeout   = 30 * time.Second
	tryMaxOutput = 4000
)

// trialReason 返回不能试调用的原因；空串表示可以。
func trialReason(s tool.Spec) string {
	switch {
	case s.Sensitive:
		return "它会读取私密内容，每次调用都要经过对话里的确认"
	case s.Risk != permission.RiskRead:
		return "它不是只读的（或你没有在安装时信任它的只读声明），请在对话里使用，每次调用会先询问你"
	case requiresArgs(s.InputSchema):
		return "它需要参数，请在对话里让模型来调用"
	}
	return ""
}

// requiresArgs 判断 JSON Schema 里有没有必填参数。解析不了的按“需要参数”处理（保守）。
func requiresArgs(schema json.RawMessage) bool {
	if len(schema) == 0 {
		return false
	}
	var s struct {
		Required []string `json:"required"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return true
	}
	return len(s.Required) > 0
}

// pluginToolNames 是这个插件真正注册进工具表的工具名（来自运行状态，不靠名字前缀猜）。
func (a *App) pluginToolNames(id string) (map[string]bool, error) {
	if a.plugins == nil {
		return nil, errNoPlugins
	}
	info, err := a.plugins.Get(id)
	if err != nil {
		return nil, err
	}
	if !info.Enabled || info.Error != "" {
		return nil, fmt.Errorf("插件 %q 未启用或无法加载", id)
	}
	d, err := a.plugins.Diagnostics(id)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, s := range d.Servers {
		for _, n := range s.Tools {
			names[n] = true
		}
	}
	return names, nil
}

// ListPluginTools 返回插件已注册的工具，以及每个能不能试调用。
func (a *App) ListPluginTools(id string) ([]PluginToolView, error) {
	names, err := a.pluginToolNames(id)
	if err != nil {
		return nil, err
	}
	out := []PluginToolView{}
	for n := range names {
		t, ok := a.engine.Tool(n)
		if !ok {
			continue
		}
		s := t.Spec()
		reason := trialReason(s)
		out = append(out, PluginToolView{Name: s.Name, Description: firstLine(s.Description), Risk: string(s.Risk), Trial: reason == "", Reason: reason})
	}
	sortToolViews(out)
	return out, nil
}

// TryPluginTool 用空参数调用插件的一个工具，让用户确认“它真的连上了、能返回结果”。
// 条件见文件开头；每次都重新检查，不信任界面传来的任何东西。
func (a *App) TryPluginTool(id, toolName string) (TryResult, error) {
	names, err := a.pluginToolNames(id)
	if err != nil {
		return TryResult{}, err
	}
	if !names[toolName] {
		return TryResult{}, fmt.Errorf("插件 %q 没有注册工具 %q", id, toolName)
	}
	t, ok := a.engine.Tool(toolName)
	if !ok {
		return TryResult{}, errors.New("这个工具已经不可用了，请刷新后再试")
	}
	if reason := trialReason(t.Spec()); reason != "" {
		return TryResult{}, errors.New("不能试调用：" + reason)
	}
	ctx, cancel := context.WithTimeout(a.context(), tryTimeout)
	defer cancel()
	start := time.Now()
	res := t.Execute(ctx, tool.Call{Input: json.RawMessage(`{}`)})
	out := res.Output
	if r := []rune(out); len(r) > tryMaxOutput {
		out = string(r[:tryMaxOutput]) + "…（已截断）"
	}
	return TryResult{Output: out, IsError: res.IsError, DurationMs: time.Since(start).Milliseconds()}, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "…"
	}
	return s
}

func sortToolViews(v []PluginToolView) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j].Name < v[j-1].Name; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
