package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultManifestURL 是默认的清单地址：GitHub 最新正式发布里的 manifest.json。
// 可以用环境变量 MYLLMAPP_UPDATE_URL 覆盖（测试、内网镜像）。
const DefaultManifestURL = "https://github.com/crazyjums/myllmapp/releases/latest/download/manifest.json"

const (
	maxManifestBytes = 1 << 20
	maxAssetBytes    = 500 << 20
)

// Info 是一次检查的结果，直接返回给前端。
type Info struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes,omitempty"`
	PubDate   string `json:"pub_date,omitempty"`
	// Releases 是当前版本之后到最新版本之间的全部更新日志（最新的在前）。
	Releases []Release `json:"releases,omitempty"`
	// Level 是更新建议：critical（含安全修复，建议尽快）/ recommended（含修复）/ optional（只有新功能）。
	Level string `json:"level,omitempty"`
	// Breaking 为 true 表示这些版本里有不兼容变更，更新前需要先看说明。
	Breaking bool `json:"breaking,omitempty"`
	// Manual 为 true 表示这个平台不能自动更新，需要用户去 Page 下载。
	Manual bool   `json:"manual"`
	Page   string `json:"page,omitempty"`
	Size   int64  `json:"size,omitempty"`

	asset Asset
}

// Updater 检查并安装更新。
type Updater struct {
	ManifestURL string
	PubKey      ed25519.PublicKey
	Current     string
	Client      *http.Client
	// GOOS / GOARCH / ExePath 默认取运行环境，测试里替换。
	GOOS, GOARCH string
	ExePath      string
}

// New 用内置公钥创建 Updater。公钥为空时 Check 返回 ErrNotConfigured。
func New() *Updater {
	u := &Updater{ManifestURL: DefaultManifestURL, Current: Version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if v := os.Getenv("MYLLMAPP_UPDATE_URL"); v != "" {
		u.ManifestURL = v
	}
	u.PubKey, _ = ParsePublicKey(embeddedPubKey)
	return u
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Enabled 说明更新功能是否可用，以及不可用的原因（开发版、没有公钥）。
func (u *Updater) Enabled() (bool, string) {
	if u.Current == "dev" || u.Current == "" {
		return false, "开发版本（版本号 dev）不检查更新。打正式包请执行：VERSION=1.0.0 ./scripts/build.sh，详见 docs/design/auto-update.md"
	}
	if u.PubKey == nil {
		return false, ErrNotConfigured.Error()
	}
	return true, ""
}

func (u *Updater) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if err := checkURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接更新服务器: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("更新服务器返回 %s（%s）", resp.Status, rawURL)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("更新清单过大")
	}
	return b, nil
}

// checkURL 只允许 https；本机回环地址允许 http（本地测试用）。
func checkURL(raw string) error {
	p, err := url.Parse(raw)
	if err != nil || p.Host == "" {
		return fmt.Errorf("更新地址不合法: %q", raw)
	}
	host := p.Hostname()
	if p.Scheme == "https" || (p.Scheme == "http" && (host == "127.0.0.1" || host == "localhost" || host == "::1")) {
		return nil
	}
	return fmt.Errorf("更新地址必须使用 https: %q", raw)
}

// Check 获取并验证清单，返回是否有新版本。
func (u *Updater) Check(ctx context.Context) (*Info, error) {
	if ok, why := u.Enabled(); !ok {
		return nil, errors.New(why)
	}
	body, err := u.get(ctx, u.ManifestURL, maxManifestBytes)
	if err != nil {
		return nil, err
	}
	sig, err := u.get(ctx, u.ManifestURL+".sig", 4096)
	if err != nil {
		return nil, err
	}
	// 先验签再解析：未验证的内容不进入任何解析逻辑。
	if err := Verify(u.PubKey, body, sig); err != nil {
		return nil, err
	}
	m, err := parseManifest(body)
	if err != nil {
		return nil, err
	}
	info := &Info{Current: u.Current, Latest: strings.TrimPrefix(m.Version, "v"), Notes: m.Notes, PubDate: m.PubDate, Page: m.Page}
	newer, err := Newer(m.Version, u.Current)
	if err != nil {
		return nil, err
	}
	if !newer {
		return info, nil
	}
	info.Available = true
	info.Releases = newerReleases(m.Releases, u.Current, m.Version)
	info.Level, info.Breaking = Advise(info.Releases)
	if len(info.Releases) == 0 {
		info.Level = "" // 清单没有结构化日志（旧格式）：不给建议，只显示 Notes
	}
	if u.GOOS == "darwin" {
		info.Manual = true
		return info, nil
	}
	a, ok := m.Platforms[u.GOOS+"-"+u.GOARCH]
	if !ok {
		// 这个平台没有安装包：仍然告诉用户有新版本，但只能手动下载。
		info.Manual = true
		return info, nil
	}
	if err := checkURL(a.URL); err != nil {
		return nil, err
	}
	if _, err := hex.DecodeString(a.SHA256); err != nil || len(a.SHA256) != sha256.Size*2 {
		return nil, errors.New("更新清单里的 sha256 不合法")
	}
	if a.Size <= 0 || a.Size > maxAssetBytes {
		return nil, errors.New("更新清单里的文件大小不合法")
	}
	info.asset, info.Size = a, a.Size
	return info, nil
}

// exePath 是要被替换的可执行文件。
func (u *Updater) exePath() (string, error) {
	if u.ExePath != "" {
		return u.ExePath, nil
	}
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// Download 把安装包下载到当前程序所在目录（保证与目标在同一个磁盘上，后面的改名才是原子的），
// 校验大小和 sha256，返回文件路径。progress 可以为 nil。
func (u *Updater) Download(ctx context.Context, info *Info, progress func(done, total int64)) (string, error) {
	if info == nil || !info.Available || info.Manual {
		return "", errors.New("没有可以自动安装的更新")
	}
	exe, err := u.exePath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	f, err := os.CreateTemp(dir, ".myllmapp-update-*")
	if err != nil {
		return "", fmt.Errorf("无法在程序目录 %s 写入更新文件（可能没有权限，请手动下载安装）: %w", dir, err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(tmp)
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.asset.URL, nil)
	if err != nil {
		return "", err
	}
	// 安装包很大，不能套用检查清单的 30 秒总超时。
	c := *u.client()
	c.Timeout = 0
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: %s", resp.Status)
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > info.asset.Size {
				return "", errors.New("下载的文件比清单声明的大，已中止")
			}
			h.Write(buf[:n])
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", werr
			}
			if progress != nil {
				progress(done, info.asset.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", fmt.Errorf("下载中断: %w", rerr)
		}
	}
	if done != info.asset.Size {
		return "", fmt.Errorf("下载的文件大小 %d 与清单 %d 不一致", done, info.asset.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, info.asset.SHA256) {
		return "", errors.New("下载的文件校验失败（sha256 不一致），已丢弃")
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	ok = true
	return tmp, nil
}

// OldSuffix 是被替换下来的旧程序的后缀（Windows 上运行中的文件不能删除，只能先改名）。
const OldSuffix = ".old"

// Apply 用 newPath 替换当前可执行文件。失败时尽量回滚到原样。
func (u *Updater) Apply(newPath string) error {
	exe, err := u.exePath()
	if err != nil {
		return err
	}
	old := exe + OldSuffix
	_ = os.Remove(old) // 上一次更新留下的
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("无法替换当前程序: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		// 回滚：把旧程序改回来，保证用户至少还有一个能用的程序。
		if rerr := os.Rename(old, exe); rerr != nil {
			return fmt.Errorf("替换失败（%v），而且无法恢复旧版本（%v）；旧版本保存在 %s", err, rerr, old)
		}
		return fmt.Errorf("替换失败，已恢复旧版本: %w", err)
	}
	return nil
}

// CleanupOld 删除上一次更新留下的旧程序。启动时调用；失败（例如旧进程还没退出）不是错误。
func CleanupOld() {
	if p, err := os.Executable(); err == nil {
		_ = os.Remove(p + OldSuffix)
	}
}
