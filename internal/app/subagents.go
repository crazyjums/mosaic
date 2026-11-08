package app

import (
	"os"

	"github.com/crazyjums/myllmapp/internal/agents"
)

// 子 agent 的设置页：列出已加载的子 agent（用户目录的、插件带来的），以及加载时的问题。
// 用户改了 ~/.myllmapp/agents/ 里的文件之后点“重新加载”即可，不用重启。

// SubagentView 是设置页上的一个子 agent。
type SubagentView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Source      string   `json:"source"` // "user" 或 "plugin:<id>"
	Tools       []string `json:"tools"`  // 空表示继承主对话的全部工具（task 除外）
	Model       string   `json:"model,omitempty"`
	MaxSteps    int      `json:"max_steps,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// SubagentsView 是设置页的内容。
type SubagentsView struct {
	Dir      string         `json:"dir"`
	Agents   []SubagentView `json:"agents"`
	Problems []string       `json:"problems"`
}

// SetAgentsDir 设置用户子 agent 的目录（装配时调用，不暴露给前端）。
func (a *App) SetAgentsDir(dir string) { a.agentsDir = dir }

// ListSubagents 返回已加载的子 agent。
func (a *App) ListSubagents() SubagentsView {
	return a.subagentsView(nil)
}

// ReloadSubagents 重新扫描用户目录（目录不存在时创建它，方便用户把文件放进去）。
func (a *App) ReloadSubagents() SubagentsView {
	if a.agentsDir == "" {
		return a.subagentsView([]string{"没有配置子 agent 目录"})
	}
	_ = os.MkdirAll(a.agentsDir, 0o755)
	defs, problems := agents.LoadDir(a.agentsDir, "")
	if skipped := a.engine.Subagents().ReplaceSource("user", defs); len(skipped) > 0 {
		for _, n := range skipped {
			problems = append(problems, "名字 "+n+" 已被插件的子 agent 占用，跳过")
		}
	}
	return a.subagentsView(problems)
}

func (a *App) subagentsView(problems []string) SubagentsView {
	out := SubagentsView{Dir: a.agentsDir, Agents: []SubagentView{}, Problems: problems}
	if out.Problems == nil {
		out.Problems = []string{}
	}
	for _, d := range a.engine.Subagents().List() {
		tools := d.Tools
		if tools == nil {
			tools = []string{}
		}
		out.Agents = append(out.Agents, SubagentView{Name: d.Name, Description: d.Description, Source: d.Source, Tools: tools, Model: d.Model, MaxSteps: d.MaxSteps, Warnings: d.Warnings})
	}
	return out
}
