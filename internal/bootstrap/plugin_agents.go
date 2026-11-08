package bootstrap

import (
	"github.com/crazyjums/myllmapp/internal/agents"
	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/plugin"
)

// pluginAgents 把插件带来的 agent 文件解析后登记到引擎的子 agent 表里（来源 "plugin:<id>"）。
// 放在装配层，是为了让 plugin 包不依赖内核：它只管“哪个插件、哪些文件、启用没有”。
type pluginAgents struct{ reg *core.SubagentRegistry }

func (p pluginAgents) ApplyPluginAgents(id, dir string, rels []string) {
	var defs []core.SubagentDefinition
	if len(rels) > 0 {
		defs, _ = agents.LoadFiles(dir, rels, id) // 有问题的文件在详情里能看到原因（DescribePluginAgents）
	}
	p.reg.ReplaceSource("plugin:"+id, defs)
}

func (p pluginAgents) DescribePluginAgents(id, dir string, rels []string) []plugin.AgentView {
	defs, problems := agents.LoadFiles(dir, rels, id)
	out := make([]plugin.AgentView, 0, len(defs)+len(problems))
	for _, d := range defs {
		out = append(out, plugin.AgentView{Name: d.Name, Description: d.Description, Warnings: d.Warnings})
	}
	// 读不了、解析失败的文件也列出来：用户在安装前就能知道哪个不会生效、为什么。
	for _, pr := range problems {
		out = append(out, plugin.AgentView{Name: "（未加载）", Warnings: []string{pr}})
	}
	return out
}
