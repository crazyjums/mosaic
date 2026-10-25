package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, cur string
		want        bool
	}{
		{"1.2.3", "1.2.2", true}, {"v1.10.0", "1.9.9", true}, {"2.0.0", "1.99.99", true},
		{"1.2.3", "1.2.3", false}, {"1.2.2", "1.2.3", false},
		{"1.0.0", "1.0.0-beta.1", true}, {"1.0.0-beta.2", "1.0.0-beta.1", false}, {"1.0.0-beta.1", "1.0.0", false},
		{"1.2.3+build5", "1.2.3", false},
	}
	for _, c := range cases {
		got, err := Newer(c.latest, c.cur)
		if err != nil || got != c.want {
			t.Errorf("Newer(%s,%s)=%v,%v 期望 %v", c.latest, c.cur, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "1.2", "a.b.c", "1.2.x"} {
		if _, err := Newer(bad, "1.0.0"); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}

// fixture 搭一个假的发布服务器：清单、签名、安装包。
type fixture struct {
	srv      *httptest.Server
	pub      ed25519.PublicKey
	priv     string
	asset    []byte
	manifest Manifest
	sigOK    bool
}

func newFixture(t *testing.T, version string) *fixture {
	t.Helper()
	pubB64, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := ParsePublicKey(pubB64)
	f := &fixture{pub: pub, priv: priv, asset: []byte("#!/bin/sh\necho new version\n"), sigOK: true}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	sum := sha256.Sum256(f.asset)
	f.manifest = Manifest{Version: version, Notes: "修复了一些问题", Page: f.srv.URL + "/page", Platforms: map[string]Asset{
		"linux-amd64":   {URL: f.srv.URL + "/app", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(f.asset))},
		"windows-amd64": {URL: f.srv.URL + "/app", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(f.asset))},
	}}
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(f.manifest)
		w.Write(b)
	})
	mux.HandleFunc("/manifest.json.sig", func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(f.manifest)
		sig, _ := Sign(f.priv, b)
		if !f.sigOK {
			sig = []byte(base64.StdEncoding.EncodeToString(make([]byte, 64)))
		}
		w.Write(sig)
	})
	mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) { w.Write(f.asset) })
	return f
}

func (f *fixture) updater(t *testing.T, current, goos string) *Updater {
	dir := t.TempDir()
	exe := filepath.Join(dir, "myllmapp")
	if err := os.WriteFile(exe, []byte("old version"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Updater{ManifestURL: f.srv.URL + "/manifest.json", PubKey: f.pub, Current: current, GOOS: goos, GOARCH: "amd64", ExePath: exe}
}

func TestCheckAndInstall(t *testing.T) {
	f := newFixture(t, "1.3.0")
	u := f.updater(t, "1.2.0", "linux")
	info, err := u.Check(context.Background())
	if err != nil || !info.Available || info.Manual || info.Latest != "1.3.0" || info.Notes == "" {
		t.Fatalf("check: %+v %v", info, err)
	}
	var last int64
	path, err := u.Download(context.Background(), info, func(done, total int64) { last = done })
	if err != nil || last != int64(len(f.asset)) {
		t.Fatalf("download: %v last=%d", err, last)
	}
	if err := u.Apply(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(u.ExePath); string(b) != string(f.asset) {
		t.Fatalf("可执行文件应被替换: %q", b)
	}
	if b, _ := os.ReadFile(u.ExePath + OldSuffix); string(b) != "old version" {
		t.Fatal("旧版本应保留为 .old")
	}
	if fi, _ := os.Stat(u.ExePath); fi.Mode()&0o100 == 0 && !strings.Contains(os.Getenv("OS"), "Windows") {
		t.Fatal("新文件应可执行")
	}
	// 下载目录里不应留下临时文件。
	entries, _ := os.ReadDir(filepath.Dir(u.ExePath))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".myllmapp-update-") {
			t.Fatalf("残留临时文件 %s", e.Name())
		}
	}
}

func TestCheckResults(t *testing.T) {
	f := newFixture(t, "1.3.0")
	// 已是最新
	if info, err := f.updater(t, "1.3.0", "linux").Check(context.Background()); err != nil || info.Available {
		t.Fatalf("已是最新: %+v %v", info, err)
	}
	// macOS：有新版本但只能手动
	info, err := f.updater(t, "1.2.0", "darwin").Check(context.Background())
	if err != nil || !info.Available || !info.Manual || info.Page == "" {
		t.Fatalf("darwin: %+v %v", info, err)
	}
	// 没有这个平台的安装包：同样只能手动
	if info, _ := f.updater(t, "1.2.0", "freebsd").Check(context.Background()); !info.Manual {
		t.Fatal("没有安装包的平台应为手动")
	}
	// 开发版不检查
	if _, err := f.updater(t, "dev", "linux").Check(context.Background()); err == nil {
		t.Fatal("开发版应报错")
	}
	// 没有公钥
	u := f.updater(t, "1.2.0", "linux")
	u.PubKey = nil
	if _, err := u.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "公钥") {
		t.Fatalf("无公钥: %v", err)
	}
}

func TestRejectsBadSignature(t *testing.T) {
	f := newFixture(t, "1.3.0")
	f.sigOK = false
	if _, err := f.updater(t, "1.2.0", "linux").Check(context.Background()); err == nil || !strings.Contains(err.Error(), "签名") {
		t.Fatalf("签名错误必须拒绝: %v", err)
	}
	// 用另一把密钥签的也要拒绝。
	f = newFixture(t, "1.3.0")
	_, other, _ := GenerateKey()
	f.priv = other
	if _, err := f.updater(t, "1.2.0", "linux").Check(context.Background()); err == nil {
		t.Fatal("别人的签名必须拒绝")
	}
}

func TestRejectsTamperedAsset(t *testing.T) {
	f := newFixture(t, "1.3.0")
	u := f.updater(t, "1.2.0", "linux")
	info, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.asset = []byte("#!/bin/sh\necho EVIL!!!!!!!\n"[:len(f.asset)]) // 同样长度、内容不同
	if len(f.asset) != int(info.Size) {
		f.asset = append(f.asset, make([]byte, int(info.Size)-len(f.asset))...)
	}
	if _, err := u.Download(context.Background(), info, nil); err == nil || !strings.Contains(err.Error(), "校验") {
		t.Fatalf("内容被篡改必须拒绝: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(u.ExePath))
	if len(entries) != 1 {
		t.Fatal("校验失败应清理临时文件")
	}
	// 服务器给的文件比声明的大
	f.asset = append([]byte("#!/bin/sh\necho new version\n"), "extra"...)
	if _, err := u.Download(context.Background(), info, nil); err == nil {
		t.Fatal("超出声明大小应中止")
	}
}

func TestApplyRollsBack(t *testing.T) {
	f := newFixture(t, "1.3.0")
	u := f.updater(t, "1.2.0", "linux")
	if err := u.Apply(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("新文件不存在应报错")
	}
	if b, _ := os.ReadFile(u.ExePath); string(b) != "old version" {
		t.Fatal("替换失败后应恢复旧版本")
	}
}

func TestCheckURL(t *testing.T) {
	for raw, ok := range map[string]bool{"https://github.com/x": true, "http://127.0.0.1:1/x": true, "http://localhost/x": true, "http://evil.com/x": false, "ftp://a/b": false, "file:///etc/passwd": false, "": false} {
		if (checkURL(raw) == nil) != ok {
			t.Errorf("checkURL(%q)", raw)
		}
	}
}

func TestParsePublicKey(t *testing.T) {
	if _, err := ParsePublicKey("  "); err != ErrNotConfigured {
		t.Fatal("空公钥应为 ErrNotConfigured")
	}
	if _, err := ParsePublicKey("not base64!!"); err == nil {
		t.Fatal("坏公钥应报错")
	}
}
