package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 插件市场：一份“有哪些插件可以装”的列表（registry.json），加上两种来源——随应用发布的内置插件和 Git 仓库。
// 列表只提供来源，不提供信任：从市场装和从本地目录装走同一条路（Preview → 用户确认 → Install），
// 所以一份被篡改的列表最多让用户看到一个陌生插件的确认框，而不能替用户装上任何东西。

const (
	registryFileName = "plugin-registry.json"
	maxRegistryBytes = 1 << 20
	registryTimeout  = 10 * time.Second
	gitTimeout       = 2 * time.Minute
	maxRegistryItems = 200
)

// CatalogEntry 是市场里的一项，给界面显示。
type CatalogEntry struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Summary map[string]string `json:"summary,omitempty"` // 语言（zh / en）→ 简介
	// Kind 是 "bundled"（随应用发布）或 "git"。
	Kind   string `json:"kind"`
	Git    string `json:"git,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Subdir string `json:"subdir,omitempty"`
	// Installed 表示已经装了同 ID 的插件，InstalledVersion 是它的版本。
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version,omitempty"`
	// Version 是市场里这一项的版本（内置插件读它自己的 plugin.json）；已安装的版本和它不同时 UpdateAvailable 为 true，
	// 界面显示“更新”：重新安装会覆盖旧版本并保留配置。
	Version         string `json:"version,omitempty"`
	UpdateAvailable bool   `json:"update_available,omitempty"`
	// Category 是分类（dev / writing / office / data / demo，界面翻译成中文或英文）；Featured 的项显示在“精选”位。
	Category string `json:"category,omitempty"`
	Featured bool   `json:"featured,omitempty"`
	// 卡片上的“它带来什么”：内置插件从它自己的 plugin.json 读出，Git 来源的在安装前不知道。
	Skills    int      `json:"skills,omitempty"`
	Commands  int      `json:"commands,omitempty"`
	Servers   int      `json:"servers,omitempty"`
	Requires  []string `json:"requires,omitempty"`
	Sensitive bool     `json:"sensitive,omitempty"`
}

type registryFile struct {
	Version int             `json:"version"`
	Plugins []registryEntry `json:"plugins"`
}

type registryEntry struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Summary map[string]string `json:"summary"`
	Bundled string            `json:"bundled,omitempty"`
	Git     string            `json:"git,omitempty"`
	Ref     string            `json:"ref,omitempty"`
	Subdir  string            `json:"subdir,omitempty"`
	// Category 和 Featured 只影响市场里的展示。
	Category string `json:"category,omitempty"`
	Featured bool   `json:"featured,omitempty"`
}

// categories 是市场里认识的分类；界面把它们翻译成中文或英文。
var categories = map[string]bool{"dev": true, "writing": true, "office": true, "data": true, "demo": true}

var (
	gitRefRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	bundledName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)
)

// isGitSource 判断用户输入的来源是不是 Git 地址。只接受 https：ssh 要用到用户的密钥，file:// 能读本机任意目录。
func isGitSource(src string) bool {
	return strings.HasPrefix(strings.ToLower(src), "https://")
}

// splitRef 把 "地址#分支或标签" 拆开。
func splitRef(src string) (string, string) {
	u, ref, _ := strings.Cut(src, "#")
	return u, ref
}

// validateGit 校验 Git 来源。地址会作为命令行参数传给 git，所以必须是 https 且不能带用户名密码，
// ref 不能以 - 开头（否则会被当成 git 的选项）。
func validateGit(rawURL, ref, subdir string) error {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return errors.New("Git 地址必须是 https:// 开头的完整地址")
	}
	if u.User != nil {
		return errors.New("Git 地址里不能带用户名或密码")
	}
	if strings.ContainsAny(rawURL, " \t\r\n\x00") || len(rawURL) > 500 {
		return errors.New("Git 地址不合法")
	}
	if ref != "" && (!gitRefRe.MatchString(ref) || strings.Contains(ref, "..")) {
		return errors.New("分支或标签名不合法")
	}
	if subdir != "" {
		if _, err := insideDir(os.TempDir(), subdir); err != nil {
			return fmt.Errorf("子目录不合法：%v", err)
		}
	}
	return nil
}

// PreviewGit 从 Git 仓库取一个插件（浅克隆，只取最新一次提交），返回安装前要给用户确认的内容。
// subdir 非空时插件在仓库的这个子目录里。克隆不会运行仓库里的任何代码（git 钩子不随仓库传输）。
func (h *Host) PreviewGit(rawURL, ref, subdir string) (Preview, error) {
	if err := validateGit(rawURL, ref, subdir); err != nil {
		return Preview{}, err
	}
	if _, err := h.opts.LookPath("git"); err != nil {
		return Preview{}, errors.New("电脑上没有找到 git，无法从 Git 地址安装；请先安装 git，或下载 zip 后安装")
	}
	label := rawURL
	if ref != "" {
		label += "#" + ref
	}
	return h.stage(label, func(stage string) error {
		clone := stage + ".clone"
		defer os.RemoveAll(clone)
		// core.symlinks=false：仓库里的符号链接只会变成普通文本文件，不会指到别处；
		// protocol.allow=never 再单独放开 https：仓库里的子模块不会被拉取，也不会走 file:// 或 ssh。
		args := []string{"-c", "core.symlinks=false", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always",
			"clone", "--depth", "1", "--single-branch", "--no-tags"}
		if ref != "" {
			args = append(args, "--branch", ref)
		}
		args = append(args, "--", rawURL, clone)
		res := runCommand(context.Background(), "", append([]string{"git"}, args...), []string{"GIT_TERMINAL_PROMPT=0"}, gitTimeout)
		if res.Err != nil {
			return fmt.Errorf("下载失败：%v", res.Err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("下载失败：%s", tail(res.Output, 300))
		}
		src := clone
		if subdir != "" {
			p, err := insideDir(clone, subdir)
			if err != nil {
				return fmt.Errorf("子目录不合法：%v", err)
			}
			src = p
		}
		if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
			return fmt.Errorf("仓库里没有目录 %q", subdir)
		}
		return copyDir(src, stage)
	})
}

// PreviewBundled 取随应用发布的内置插件。
func (h *Host) PreviewBundled(name string) (Preview, error) {
	sub, err := h.bundledFS(name)
	if err != nil {
		return Preview{}, err
	}
	return h.stage("bundled:"+name, func(stage string) error {
		return fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			dst := filepath.Join(stage, filepath.FromSlash(p))
			if d.IsDir() {
				return os.MkdirAll(dst, 0o755)
			}
			data, err := fs.ReadFile(sub, p)
			if err != nil {
				return err
			}
			return os.WriteFile(dst, data, 0o644)
		})
	})
}

// bundledFS 返回内置插件的目录；名字必须是已嵌入的目录名（在线列表不能借此读到别的路径）。
func (h *Host) bundledFS(name string) (fs.FS, error) {
	if h.opts.Catalog == nil || !bundledName.MatchString(name) {
		return nil, fmt.Errorf("没有内置插件 %q", name)
	}
	sub, err := fs.Sub(h.opts.Catalog, path.Join("bundled", name))
	if err != nil {
		return nil, fmt.Errorf("没有内置插件 %q", name)
	}
	if _, err := fs.Stat(sub, FileName); err != nil {
		return nil, fmt.Errorf("没有内置插件 %q", name)
	}
	return sub, nil
}

// Catalog 返回市场列表：先是内置列表，再是在线列表里（上次刷新缓存下来的）新增或覆盖的项。
func (h *Host) Catalog() []CatalogEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.catalogLocked()
}

func (h *Host) catalogLocked() []CatalogEntry {
	var order []string
	byID := map[string]registryEntry{}
	for _, list := range [][]registryEntry{h.builtinRegistry(), h.remote} {
		for _, e := range list {
			if _, ok := byID[e.ID]; !ok {
				order = append(order, e.ID)
			}
			byID[e.ID] = e
		}
	}
	out := make([]CatalogEntry, 0, len(order))
	for _, id := range order {
		e := byID[id]
		c := CatalogEntry{ID: e.ID, Name: e.Name, Summary: e.Summary, Kind: "git", Git: e.Git, Ref: e.Ref, Subdir: e.Subdir, Category: e.Category, Featured: e.Featured}
		if e.Bundled != "" {
			c.Kind = "bundled"
			h.fillBundled(&c, e.Bundled)
		}
		if l, ok := h.plugins[id]; ok && l.m != nil {
			c.Installed, c.InstalledVersion = true, l.m.Version
			c.UpdateAvailable = c.Version != "" && c.Version != l.m.Version
		}
		out = append(out, c)
	}
	return out
}

// fillBundled 读取内置插件自己的 plugin.json，填上版本和“带来什么”（技能 / 命令 / 工具的数量、依赖、是否读取私密内容）；
// 读不到就留空，卡片上少显示一些信息而已。
func (h *Host) fillBundled(c *CatalogEntry, name string) {
	sub, err := h.bundledFS(name)
	if err != nil {
		return
	}
	data, err := fs.ReadFile(sub, FileName)
	if err != nil {
		return
	}
	var v struct {
		Version  string `json:"version"`
		Skills   []any  `json:"skills"`
		Commands []any  `json:"commands"`
		MCP      []struct {
			Sensitive bool `json:"sensitive"`
		} `json:"mcp"`
		Requires struct {
			Commands []string `json:"commands"`
		} `json:"requires"`
	}
	if json.Unmarshal(data, &v) != nil {
		return
	}
	c.Version, c.Skills, c.Commands, c.Servers, c.Requires = v.Version, len(v.Skills), len(v.Commands), len(v.MCP), v.Requires.Commands
	for _, m := range v.MCP {
		c.Sensitive = c.Sensitive || m.Sensitive
	}
}

// builtinRegistry 读取随应用发布的列表；读不到（没有嵌入、格式坏了）就当作没有。
func (h *Host) builtinRegistry() []registryEntry {
	if h.opts.Catalog == nil {
		return nil
	}
	data, err := fs.ReadFile(h.opts.Catalog, "registry.json")
	if err != nil {
		return nil
	}
	return h.parseRegistry(data)
}

// parseRegistry 解析并逐项校验列表：坏的项被丢掉，不影响其他项。
func (h *Host) parseRegistry(data []byte) []registryEntry {
	var rf registryFile
	if json.Unmarshal(data, &rf) != nil || rf.Version != 1 {
		return nil
	}
	var out []registryEntry
	seen := map[string]bool{}
	for _, e := range rf.Plugins {
		if len(out) >= maxRegistryItems {
			break
		}
		if !idRe.MatchString(e.ID) || seen[e.ID] || e.Name == "" || len([]rune(e.Name)) > 60 {
			continue
		}
		for k, v := range e.Summary {
			if len([]rune(v)) > 300 {
				e.Summary[k] = string([]rune(v)[:300])
			}
		}
		if !categories[e.Category] {
			e.Category = "" // 不认识的分类当作没有分类，不影响安装
		}
		switch {
		case e.Bundled != "" && e.Git == "":
			if _, err := h.bundledFS(e.Bundled); err != nil {
				continue
			}
			e.Ref, e.Subdir = "", ""
		case e.Git != "" && e.Bundled == "":
			if validateGit(e.Git, e.Ref, e.Subdir) != nil {
				continue
			}
		default:
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	return out
}

// RefreshCatalog 从在线地址拉取最新列表并缓存到磁盘，返回合并后的市场。没有配置地址时直接返回当前列表。
// 失败（离线、地址不通、内容不对）时返回错误，已有的列表不变。
func (h *Host) RefreshCatalog(ctx context.Context) ([]CatalogEntry, error) {
	if h.opts.RegistryURL == "" {
		return h.Catalog(), nil
	}
	ctx, cancel := context.WithTimeout(ctx, registryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.opts.RegistryURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("刷新插件列表失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("刷新插件列表失败：HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRegistryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("刷新插件列表失败：%v", err)
	}
	if len(data) > maxRegistryBytes {
		return nil, errors.New("刷新插件列表失败：内容过大")
	}
	entries := h.parseRegistry(data)
	if entries == nil {
		return nil, errors.New("刷新插件列表失败：内容格式不对")
	}
	h.mu.Lock()
	h.remote = entries
	list := h.catalogLocked()
	h.mu.Unlock()
	// 缓存只是为了下次离线启动也能看到上次的列表；写失败不影响本次。
	_ = writeFileAtomic(h.registryCache(), data)
	return list, nil
}

func (h *Host) registryCache() string {
	return filepath.Join(filepath.Dir(h.opts.StateFile), registryFileName)
}

// loadRegistryCache 读取上次刷新缓存的列表（启动时调用）。
func (h *Host) loadRegistryCache() {
	data, err := os.ReadFile(h.registryCache())
	if err != nil || len(data) > maxRegistryBytes {
		return
	}
	h.remote = h.parseRegistry(data)
}

// PreviewCatalog 按市场里的 ID 取插件，返回安装前要给用户确认的内容。
func (h *Host) PreviewCatalog(id string) (Preview, error) {
	h.mu.Lock()
	var entry *CatalogEntry
	for _, c := range h.catalogLocked() {
		if c.ID == id {
			c := c
			entry = &c
			break
		}
	}
	h.mu.Unlock()
	if entry == nil {
		return Preview{}, fmt.Errorf("市场里没有插件 %q", id)
	}
	if entry.Kind == "bundled" {
		return h.PreviewBundled(h.bundledNameFor(id))
	}
	return h.PreviewGit(entry.Git, entry.Ref, entry.Subdir)
}

// bundledNameFor 返回市场项 id 对应的内置目录名（列表里写的 bundled 字段，通常和 id 相同）。
func (h *Host) bundledNameFor(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, list := range [][]registryEntry{h.remote, h.builtinRegistry()} {
		for _, e := range list {
			if e.ID == id && e.Bundled != "" {
				return e.Bundled
			}
		}
	}
	return id
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}
