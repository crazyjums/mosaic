package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/crazyjums/myllmapp/internal/app"
	"github.com/crazyjums/myllmapp/internal/bootstrap"
	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/harness/remote"
	"github.com/crazyjums/myllmapp/internal/rpc"
	"github.com/crazyjums/myllmapp/internal/update"
)

// ProtocolVersion 是 mosaicd 与 Electron 主进程之间协议的版本；不兼容的改动要加一，主进程握手时检查。
const ProtocolVersion = 1

// excluded 是 App 上不能让前端调用的方法（生命周期、装配用的）。
var excluded = []string{"Startup", "Shutdown", "SetHost", "SetPlugins", "SetChangelog", "UseShellUpdater"}

func main() {
	cfgPath := flag.String("config", "", "配置文件路径（默认查找 ./config.toml、~/.myllmapp/config.toml）")
	workspace := flag.String("workspace", "", "工作区目录，覆盖配置（一般不传，和 Wails 版一样用界面里选的）")
	changelogPath := flag.String("changelog", "", "CHANGELOG.md 路径（设置 → 关于里的版本历史；Electron 把它作为资源一起发布）")
	demo := flag.Bool("demo", false, "启用离线演示厂商（不需要密钥，开发和冒烟测试用）")
	flag.Parse()

	// 协议独占真正的 stdout；把 os.Stdout 换成 stderr，任何库的意外打印都不会混进协议里。
	protocolOut := os.Stdout
	os.Stdout = os.Stderr
	log.SetOutput(os.Stderr)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	if *demo {
		cfg.EnableDemo()
	}
	ws, err := bootstrap.DesktopWorkspace(cfg)
	if err != nil {
		log.Fatal(err)
	}
	built, err := bootstrap.Build(cfg, bootstrap.Options{Workspace: *workspace, FallbackWorkspace: ws})
	if err != nil {
		log.Fatal(err)
	}
	defer built.Close()

	var changelog string
	if *changelogPath != "" {
		if b, err := os.ReadFile(*changelogPath); err == nil {
			changelog = string(b)
		} else {
			log.Printf("读取 %s 失败：%v（版本历史将为空）", *changelogPath, err)
		}
	}
	if err := serve(context.Background(), built, changelog, os.Stdin, protocolOut); err != nil {
		log.Fatal(err)
	}
}

// serve 组装绑定层并在 in / out 上服务，直到 in 读到 EOF。测试直接调用它。
func serve(ctx context.Context, built *bootstrap.Built, changelog string, in io.Reader, out io.Writer) error {
	a := app.New(built.Engine, built.Settings, nil)
	a.SetPlugins(built.Plugins)
	a.SetChangelog(changelog)
	// 更新由 Electron 壳安装：本程序只是安装包里的一个子程序，不能自己替换自己。
	// 安装包下载到用户私有的缓存目录（程序目录通常不可写）。
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	a.UseShellUpdater(filepath.Join(cache, "mosaic", "updates"))

	methods := rpc.NewMethods(a, excluded...)
	hello := map[string]any{
		"protocol":       ProtocolVersion,
		"event_protocol": event.ProtocolVersion,
		"version":        update.Version,
		"methods":        methods.Names(),
	}
	// 远程引擎的 Hub 要用到 peer，peer 的处理函数又要用到 Hub：先声明，peer 建好后再赋值。
	var hub *remote.Hub
	peer := rpc.NewPeer(out, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == "rpc.hello" {
			return hello, nil
		}
		if isHarnessMethod(method) {
			return harnessCall(ctx, built.Engine, hub, method, params)
		}
		return methods.Call(ctx, method, params)
	})
	hub = remote.NewHub(&harnessHost{peer: peer})
	a.SetHost(&host{peer: peer})
	a.Startup(ctx)
	defer a.Shutdown(ctx)
	return peer.Serve(ctx, in)
}
