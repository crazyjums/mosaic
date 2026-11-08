package bootstrap

import (
	"time"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/hooks"
	"github.com/crazyjums/myllmapp/internal/plugin"
)

// pluginHooks 把插件里用户批准过的钩子交给钩子运行器（来源 "plugin:<id>"）。
// plugin 包只管“哪些钩子、批准了没有”，怎么执行在 internal/hooks，两边在装配层接上。
type pluginHooks struct{ runner *hooks.Runner }

func (p pluginHooks) ApplyPluginHooks(id, name, dir, dataDir string, decls []plugin.HookDecl) {
	hs := make([]hooks.Hook, 0, len(decls))
	for _, d := range decls {
		hs = append(hs, hooks.Hook{
			ID: d.ID, SourceName: name, Event: core.HookEvent(d.Event), Matcher: d.Matcher,
			Command: d.Command, Timeout: time.Duration(d.Timeout) * time.Second,
			PluginRoot: dir, PluginData: dataDir,
		})
	}
	p.runner.ReplaceSource("plugin:"+id, hs)
}

func (p pluginHooks) SetHooksDisabled(off bool) { p.runner.SetDisabled(off) }
