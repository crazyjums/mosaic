//go:build desktop || dev || production || bindings

// 桌面端入口（Wails v2）。
//
// 构建标签说明：Wails CLI 构建时会自动带上标签——`wails build` 带 desktop,production，
// `wails dev` 带 dev，生成前端绑定时带 bindings。普通的 `go build ./...`、`go vet ./...`、
// `go test ./...` 不带这些标签，因此会跳过本文件，也就不需要 WebKit 等系统依赖。
// 前端资源的嵌入放在 assets_*.go 里，原因见那两个文件的注释。
//
// 本文件只做三件事：读配置 → 组装内核 → 把绑定对象交给 Wails。与 cmd/cli/main.go 对照阅读，
// 可以看出两个入口共享同一套 bootstrap，区别只在"界面"。
package main

import (
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/crazyjums/myllmapp/internal/app"
	"github.com/crazyjums/myllmapp/internal/bootstrap"
	"github.com/crazyjums/myllmapp/internal/config"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatal(err)
	}
	built, err := bootstrap.Build(cfg, bootstrap.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer built.Close()

	a := app.New(built.Engine, built.Settings, nil)
	err = wails.Run(&options.App{
		Title:            "myllmapp",
		Width:            1280,
		Height:           840,
		MinWidth:         900,
		MinHeight:        600,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 21, A: 1},
		OnStartup:        a.Startup,
		OnShutdown:       a.Shutdown,
		Bind:             []interface{}{a},
	})
	if err != nil {
		log.Fatal(err)
	}
}
