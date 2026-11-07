package remote

import (
	"context"
	"errors"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// 远程引擎的厂商类型。配置档需要一个 Provider 才能进目录；真正的工作由 Hub 提供的 Harness 完成。
// 没有桌面壳（命令行、Wails 版）时引擎不会被注册，这时调用到 Stream 会得到下面的提示。
const (
	TypeClaudeCode = "claudecode"
	TypePi         = "pi"
)

var labels = map[string]string{TypeClaudeCode: "Claude Code", TypePi: "pi"}

func init() {
	for typ := range labels {
		typ := typ
		provider.Register(typ, func(cfg provider.ProfileConfig) (provider.Provider, error) {
			return stub{name: cfg.Name, typ: typ}, nil
		})
	}
}

type stub struct{ name, typ string }

func (s stub) Name() string { return s.name }
func (s stub) Stream(context.Context, provider.Request, func(provider.Delta)) error {
	return errors.New(labels[s.typ] + " 引擎只在 Mosaic 的 Electron 版里可用（它的官方 SDK 跑在 Electron 的后台进程里）；当前环境请改用其他厂商，或安装 Electron 版")
}
