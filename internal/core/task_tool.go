package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// TaskToolName 是派子 agent 的工具名。
const TaskToolName = "task"

// 子 agent 默认的最大步数：派出去的活应该是一件具体的小事，不是另一整轮对话。
// 定义里写了更大的 maxTurns 也最多 maxSubagentSteps 步（并且不超过主 agent 的上限）。
const (
	defaultSubagentSteps = 20
	maxSubagentSteps     = 100
)

// taskTool 把子 agent 定义包装成主 agent 可以调用的工具，见 subagent.go 开头的说明。
type taskTool struct{ e *Engine }

// availability 由 Agent.toolSpecs 检查：一个子 agent 都没有时，不把 task 发给模型（免得多一个没用的工具）。
type availability interface{ Available() bool }

func (t *taskTool) Available() bool { return len(t.e.cfg.Subagents.List()) > 0 }

func (t *taskTool) Spec() tool.Spec {
	defs := t.e.cfg.Subagents.List()
	var b strings.Builder
	b.WriteString("把一件具体的任务派给一个子 agent。子 agent 在独立的上下文里工作（看不到这段对话），用自己的提示词和工具完成后，只把结论交回来。适合会产生大量中间输出的活：查资料、跑测试、审代码。prompt 要写清楚任务、需要的背景和希望得到什么样的结论。\n\n可用的子 agent：")
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
		fmt.Fprintf(&b, "\n- %s：%s", d.Name, d.Description)
	}
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent":  map[string]any{"type": "string", "enum": names, "description": "子 agent 的名字"},
			"prompt": map[string]any{"type": "string", "description": "交给子 agent 的任务说明（它看不到这段对话，背景要写全）"},
		},
		"required": []string{"agent", "prompt"},
	})
	// 派子 agent 这件事本身不读写任何东西；子 agent 的每个工具调用照常走权限。
	return tool.Spec{Name: TaskToolName, Description: b.String(), InputSchema: schema, Risk: permission.RiskRead}
}

type taskInput struct {
	Agent  string `json:"agent"`
	Prompt string `json:"prompt"`
}

func (t *taskTool) Summarize(input json.RawMessage) string {
	var in taskInput
	_ = json.Unmarshal(input, &in)
	p := []rune(strings.TrimSpace(in.Prompt))
	if len(p) > 60 {
		p = append(p[:60], '…')
	}
	return fmt.Sprintf("派给子 agent %s：%s", in.Agent, string(p))
}

func (t *taskTool) Execute(ctx context.Context, call tool.Call) tool.Result {
	var in taskInput
	if err := json.Unmarshal(call.Input, &in); err != nil || strings.TrimSpace(in.Agent) == "" || strings.TrimSpace(in.Prompt) == "" {
		return tool.Errorf("task 需要 agent 和 prompt 两个参数")
	}
	def, ok := t.e.cfg.Subagents.Get(in.Agent)
	if !ok {
		return tool.Errorf("没有名为 %q 的子 agent", in.Agent)
	}
	t.e.mu.Lock()
	r := t.e.runs[call.SessionID]
	t.e.mu.Unlock()
	if r == nil || r.cfg.Provider == nil {
		return tool.Errorf("task 只能在对话的一轮里调用")
	}
	parent := r.cfg
	if parent.ParentID != "" {
		return tool.Errorf("子 agent 不能再派子 agent")
	}

	child := parent
	child.ID = event.NewID("agent")
	child.ParentID = parent.ID
	child.AgentType = def.Name
	// 子 agent 看不到这段对话，但项目约定（AGENTS.md 等）和技能清单是所有 agent 共享的：不给它，
	// 它可能做出和项目约定冲突的改动。所以它自己的提示词后面接上工作区、项目约定和技能清单。
	child.System = def.System + "\n\nWorkspace (current working directory): " + parent.Workspace + ProjectContext(parent.Workspace)
	if t.e.cfg.SkillsPrompt != nil {
		child.System += t.e.cfg.SkillsPrompt(parent.Workspace)
	}
	child.Compactor = NoopCompactor{} // 派出去的是一件小事：上下文从零开始，不需要压缩
	child.MaxSteps = def.MaxSteps
	switch {
	case child.MaxSteps <= 0:
		child.MaxSteps = defaultSubagentSteps
	case child.MaxSteps > maxSubagentSteps:
		child.MaxSteps = maxSubagentSteps
	}
	if parent.MaxSteps > 0 && child.MaxSteps > parent.MaxSteps {
		child.MaxSteps = parent.MaxSteps
	}
	// 模型：只有当前厂商配置档里确实有这个模型时才换，否则沿用主对话的模型。
	if m := resolveModel(def.Model, r.profile.Models); m != "" {
		child.Model = m
	}
	reg, err := subagentTools(parent.Tools, def)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	child.Tools = reg

	em := emitter{pub: parent.Publisher, sessionID: call.SessionID, agentID: parent.ID, parentID: parent.ParentID}
	em.emit("", event.SubagentStart{AgentID: child.ID, ParentID: parent.ID, Name: def.Name, Task: in.Prompt, CallID: call.ID})

	// SubagentStart 钩子：不能阻止，只能给子 agent 补一段带标注的上下文（接在任务说明后面）。
	prompt := in.Prompt
	if parent.Hooks != nil {
		ho := runHooks(ctx, parent.Hooks, em, HookInput{Event: HookSubagentStart, Cwd: parent.Workspace, AgentID: child.ID, AgentType: def.Name, Prompt: in.Prompt}, call.ID)
		if s := hookContextText(ho.Context); s != "" {
			prompt += "\n\n" + s
		}
	}

	// 子 agent 自己的上下文：会话 ID 和主对话相同（事件要送到同一个会话的界面上），但历史是空的，也不保存。
	sess := NewSession(call.SessionID, r.sess.Snapshot().Provider, child.Model)
	text, res := NewAgent(child).RunSubagent(ctx, sess, prompt)
	r.sess.addUsage(res.Usage)

	status := "completed"
	switch res.Reason {
	case event.TurnCompleted:
	case event.TurnCancelled:
		status = "cancelled"
	default:
		status = "failed"
	}
	em.emit("", event.SubagentEnd{AgentID: child.ID, ParentID: parent.ID, Status: status, Summary: firstRunes(text, 200)})

	switch {
	case res.Reason == event.TurnCancelled:
		return tool.Errorf("子 agent %s 被停止了", def.Name)
	case res.Reason == event.TurnMaxSteps:
		return tool.Result{Output: fmt.Sprintf("（子 agent %s 达到了 %d 步的上限，下面是它停下时的结论，可能不完整）\n\n%s", def.Name, child.MaxSteps, orNone(text))}
	case res.Reason != event.TurnCompleted:
		msg := "子 agent " + def.Name + " 出错了"
		if res.Err != nil {
			msg += "：" + res.Err.Error()
		}
		return tool.Result{Output: msg + "\n\n" + orNone(text), IsError: true}
	}
	return tool.Result{Output: orNone(text)}
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（子 agent 没有给出文字结论）"
	}
	return s
}

func firstRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// subagentTools 为子 agent 组一张工具表：从主 agent 的工具里去掉 task（不允许嵌套）、去掉黑名单，
// 有白名单时只留白名单里的。白名单写了却一个都对不上时拒绝运行——不悄悄给它全部工具。
func subagentTools(all *tool.Registry, def SubagentDefinition) (*tool.Registry, error) {
	reg := tool.NewRegistry()
	for _, s := range all.Specs() {
		if s.Name == TaskToolName || slices.Contains(def.DisallowedTools, s.Name) {
			continue
		}
		if len(def.Tools) > 0 && !slices.Contains(def.Tools, s.Name) {
			continue
		}
		t, _ := all.Get(s.Name)
		_ = reg.Register(t)
	}
	if len(def.Tools) > 0 && len(reg.Specs()) == 0 {
		names := slices.DeleteFunc(slices.Clone(def.Tools), func(n string) bool { return strings.HasPrefix(n, "__") })
		if len(names) == 0 {
			return nil, fmt.Errorf("子 agent %s 的工具白名单一个都对不上 Mosaic 的工具，为安全起见不运行", def.Name)
		}
		return nil, fmt.Errorf("子 agent %s 的工具白名单一个都对不上现有的工具（%s），为安全起见不运行", def.Name, strings.Join(names, ", "))
	}
	return reg, nil
}

// resolveModel 在厂商配置档的模型里找子 agent 定义要的模型：先找完全相同的 ID；
// 再把 Claude Code 的别名（sonnet / opus / haiku）对应到名字里含这个词的模型（取列表里第一个）。找不到返回空串。
func resolveModel(want string, models []string) string {
	want = strings.TrimSpace(want)
	if want == "" || want == "inherit" {
		return ""
	}
	if slices.Contains(models, want) {
		return want
	}
	switch strings.ToLower(want) {
	case "sonnet", "opus", "haiku":
		for _, m := range models {
			if strings.Contains(strings.ToLower(m), strings.ToLower(want)) {
				return m
			}
		}
	}
	return ""
}
