//go:build !windows

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/bootstrap"
	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/update"
)

// 端到端：假的发布服务器 → 检查 → 安装 → 可执行文件被替换 → 调用退出。
func TestCheckAndInstallUpdate(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	built, err := bootstrap.Build(cfg, bootstrap.Options{Workspace: t.TempDir(), NoStore: true})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()

	pubB64, priv, _ := update.GenerateKey()
	pub, _ := update.ParsePublicKey(pubB64)
	asset := []byte("#!/bin/sh\nexit 0\n")
	sum := sha256.Sum256(asset)
	var m update.Manifest
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	m = update.Manifest{Version: "2.0.0", Notes: "新功能", Platforms: map[string]update.Asset{
		"linux-amd64": {URL: srv.URL + "/app", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(asset))},
	}}
	mux.HandleFunc("/m.json", func(w http.ResponseWriter, _ *http.Request) { b, _ := json.Marshal(m); w.Write(b) })
	mux.HandleFunc("/m.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		b, _ := json.Marshal(m)
		sig, _ := update.Sign(priv, b)
		w.Write(sig)
	})
	mux.HandleFunc("/app", func(w http.ResponseWriter, _ *http.Request) { w.Write(asset) })

	exe := filepath.Join(t.TempDir(), "app")
	_ = os.WriteFile(exe, []byte("old"), 0o755)
	a := New(built.Engine, built.Settings, func(context.Context, string, ...any) {})
	a.upd = &update.Updater{ManifestURL: srv.URL + "/m.json", PubKey: pub, Current: "1.0.0", GOOS: "linux", GOARCH: "amd64", ExePath: exe}
	quit := make(chan struct{})
	a.us.quit = func(context.Context) { close(quit) }

	if err := a.InstallUpdate(); err == nil {
		t.Fatal("没有检查过就安装应报错")
	}
	info, err := a.CheckUpdate()
	if err != nil || !info.Available || info.Latest != "2.0.0" || info.Disabled != "" {
		t.Fatalf("check: %+v %v", info, err)
	}
	if err := a.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-quit:
	case <-time.After(10 * time.Second):
		t.Fatalf("安装没有完成: %+v", a.GetUpdateStatus())
	}
	if b, _ := os.ReadFile(exe); string(b) != string(asset) {
		t.Fatalf("应已替换: %q", b)
	}
	if st := a.GetUpdateStatus(); st.State != "restarting" {
		t.Fatalf("状态: %+v", st)
	}
}

func TestUpdateDisabledForDevBuild(t *testing.T) {
	a := New(nil, nil, func(context.Context, string, ...any) {})
	a.upd = &update.Updater{Current: "dev"}
	info, err := a.CheckUpdate()
	if err != nil || info.Disabled == "" {
		t.Fatalf("开发版应显示不可用: %+v %v", info, err)
	}
}
