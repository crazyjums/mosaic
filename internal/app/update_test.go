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
	"sync/atomic"
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

func TestGetVersionHistory(t *testing.T) {
	a := &App{}
	if got := a.GetVersionHistory(); len(got) != 0 {
		t.Fatalf("没有设置日志时应为空: %v", got)
	}
	a.SetChangelog("# 更新日志\n\n## [Unreleased]\n\n### 新增\n- 未发布\n\n## [1.1.0] - 2026-10-25\n\n### 新增\n- 甲\n\n## [1.0.0] - 2026-10-01\n\n### 修复\n- 乙\n")
	got := a.GetVersionHistory()
	if len(got) != 2 || got[0].Version != "1.1.0" || got[1].Version != "1.0.0" {
		t.Fatalf("应跳过 Unreleased，最新在前: %+v", got)
	}
}

func TestCheckUpdateUsesSavedGlobalProxy(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	built, err := bootstrap.Build(cfg, bootstrap.Options{Workspace: t.TempDir(), NoStore: true})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()

	pubB64, priv, err := update.GenerateKey()
	if err != nil { t.Fatal(err) }
	pub, err := update.ParsePublicKey(pubB64)
	if err != nil { t.Fatal(err) }
	m := update.Manifest{Version: "1.1.0", Platforms: map[string]update.Asset{}}
	body, _ := json.Marshal(m)
	sig, err := update.Sign(priv, body)
	if err != nil { t.Fatal(err) }

	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests.Add(1)
		if !r.URL.IsAbs() || r.URL.Host != "127.0.0.1:1" {
			http.Error(w, "not routed via configured proxy", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/m.json":
			_, _ = w.Write(body)
		case "/m.json.sig":
			_, _ = w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	}))
	defer proxy.Close()

	a := New(built.Engine, built.Settings, func(context.Context, string, ...any) {})
	// 无监听端口的更新服务器，只有通过用户配置的应用代理才可能成功。
	a.upd = &update.Updater{ManifestURL: "http://127.0.0.1:1/m.json", PubKey: pub, Current: "1.0.0", GOOS: "darwin", GOARCH: "arm64"}
	if err := a.SetProxy(proxy.URL); err != nil { t.Fatal(err) }
	result, err := a.CheckUpdate()
	if err != nil || !result.Available || result.Latest != "1.1.0" {
		t.Fatalf("更新检查应经全局代理成功: %+v %v", result, err)
	}
	if got := proxyRequests.Load(); got != 2 {
		t.Fatalf("清单与签名都应通过代理，实际 %d 次", got)
	}
	if err := a.SetProxy(""); err != nil { t.Fatal(err) }
	if _, err := a.CheckUpdate(); err == nil {
		t.Fatal("清空代理后应回到直连（127.0.0.1:1 无服务）")
	}
}

type installerHost struct {
	Host
	got chan string
}

func (h *installerHost) InstallUpdate(_ context.Context, path string) error {
	h.got <- path
	return nil
}

// Electron 版：后端下载并校验安装包，再交给壳运行；不替换自己，也不自己重启。
func TestShellUpdater(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	built, err := bootstrap.Build(cfg, bootstrap.Options{Workspace: t.TempDir(), NoStore: true})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()

	pubB64, priv, _ := update.GenerateKey()
	pub, _ := update.ParsePublicKey(pubB64)
	asset := []byte("installer")
	sum := sha256.Sum256(asset)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	m := update.Manifest{Version: "2.0.0", Notes: "新功能", Platforms: map[string]update.Asset{
		"linux-amd64-electron": {URL: srv.URL + "/pkg.AppImage", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(asset))},
	}}
	mux.HandleFunc("/m.json", func(w http.ResponseWriter, _ *http.Request) { b, _ := json.Marshal(m); w.Write(b) })
	mux.HandleFunc("/m.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		b, _ := json.Marshal(m)
		sig, _ := update.Sign(priv, b)
		w.Write(sig)
	})
	mux.HandleFunc("/pkg.AppImage", func(w http.ResponseWriter, _ *http.Request) { w.Write(asset) })

	exe := filepath.Join(t.TempDir(), "mosaicd")
	_ = os.WriteFile(exe, []byte("old"), 0o755)
	dir := filepath.Join(t.TempDir(), "updates")
	a := New(built.Engine, built.Settings, func(context.Context, string, ...any) {})
	a.upd = &update.Updater{ManifestURL: srv.URL + "/m.json", PubKey: pub, Current: "1.0.0", GOOS: "linux", GOARCH: "amd64", ExePath: exe}
	a.UseShellUpdater(dir)

	if _, err := a.CheckUpdate(); err != nil {
		t.Fatal(err)
	}
	// 壳不支持安装时要明确拒绝，而不是下载完才失败。
	if err := a.InstallUpdate(); err == nil {
		t.Fatal("没有支持安装的壳时应拒绝")
	}
	h := &installerHost{got: make(chan string, 1)}
	a.SetHost(h)
	if err := a.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-h.got:
		if filepath.Dir(p) != dir || filepath.Ext(p) != ".AppImage" {
			t.Fatalf("安装包位置不对: %s", p)
		}
		if b, _ := os.ReadFile(p); string(b) != string(asset) {
			t.Fatal("安装包内容不对")
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("没有交给壳安装: %+v", a.GetUpdateStatus())
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatal("后端不应替换自己")
	}
}
