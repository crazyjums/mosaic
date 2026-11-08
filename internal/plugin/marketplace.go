package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Claude Code 的 marketplace：一个仓库（或文件夹）里放多个插件，根目录有 .claude-plugin/marketplace.json 列出它们。
//
// 格式依据 Claude Code 官方的 marketplace 参考（marketplace-reference）：
//   - 顶层：name、owner、plugins[]（必填），description、metadata.pluginRoot 等
//   - 条目：name 和 source 必填；source 是相对路径字符串（./… 或 "."，在 metadata.pluginRoot 下的裸名也行），
//     或对象 {source: github|url|git-subdir|npm|archive|command, …}
//
// Mosaic 的做法：用户给一个 Git 地址或文件夹，如果它是 marketplace，就列出里面的插件让用户挑一个，挑中之后走和
// 其他来源完全相同的 预览 → 确认 → 安装。支持相对路径、github、url、git-subdir；npm、archive、command 暂不支持，
// 在列表里灰显并写明原因（command 会在本机运行命令，Mosaic 不做）。
// 列出和读取条目不运行任何东西；取某个插件时才去克隆它的仓库。

const marketplaceFile = ".claude-plugin/marketplace.json"

const maxMarketEntries = 200

var (
	marketNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	repoRe       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
)

// 条目的来源类型。
const (
	srcRelative = "relative"
	srcGitHub   = "github"
	srcURL      = "url"
	srcSubdir   = "git-subdir"
	srcNPM      = "npm"
	srcArchive  = "archive"
	srcCommand  = "command"
)

// MarketPlugin 是 marketplace 里的一项，选择界面用它渲染。
type MarketPlugin struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	// Kind 是来源类型；Where 是人能看懂的来源（路径或仓库），选之前就让用户知道插件从哪来。
	Kind  string `json:"kind"`
	Where string `json:"where"`
	// Supported 为 false 时不能选，Reason 说明原因。
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

// MarketInfo 是一个 marketplace 的概况。
type MarketInfo struct {
	Token       string         `json:"token"`
	Name        string         `json:"name"`
	Owner       string         `json:"owner,omitempty"`
	Description string         `json:"description,omitempty"`
	Source      string         `json:"source"`
	Plugins     []MarketPlugin `json:"plugins"`
}

// SourceInspection 是“看一眼这个来源是什么”的结果：一个插件（已经预览好，可以直接确认安装），
// 或者一个 marketplace（要先挑一个插件）。
type SourceInspection struct {
	Kind    string      `json:"kind"` // "plugin" / "marketplace"
	Preview *Preview    `json:"preview,omitempty"`
	Market  *MarketInfo `json:"market,omitempty"`
}

// marketEntry 是解析好的条目，带着取这个插件需要的全部信息。
type marketEntry struct {
	MarketPlugin
	rel                    string // 相对路径来源：marketplace 根目录下的子目录（"." 是根本身）
	gitURL, ref, sha, path string // 远程来源
}

type stagedMarket struct {
	root    string // marketplace 的根目录
	owned   bool   // 根目录是我们克隆的临时目录，取消时要删掉
	label   string
	entries map[string]*marketEntry
	at      time.Time
}

type marketFileJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Owner       struct {
		Name string `json:"name"`
	} `json:"owner"`
	Metadata struct {
		Description string `json:"description"`
		PluginRoot  string `json:"pluginRoot"`
	} `json:"metadata"`
	Plugins []json.RawMessage `json:"plugins"`
}

type marketEntryJSON struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Source      json.RawMessage `json:"source"`
}

// hasMarketplace 判断目录是不是 marketplace 的根。
func hasMarketplace(dir string) bool {
	return fileExists(filepath.Join(dir, filepath.FromSlash(marketplaceFile)))
}

// parseMarketplace 读 root 下的 marketplace.json，返回概况和各条目。不运行任何东西。
func parseMarketplace(root string) (*MarketInfo, map[string]*marketEntry, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(marketplaceFile)))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > maxManifestBytes*4 {
		return nil, nil, errors.New("marketplace.json 太大")
	}
	var f marketFileJSON
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, nil, fmt.Errorf("marketplace.json 不是合法的 JSON：%v", err)
	}
	if !marketNameRe.MatchString(f.Name) {
		return nil, nil, fmt.Errorf("marketplace 的 name %q 不合法（字母、数字、. _ -，以字母或数字开头）", f.Name)
	}
	info := &MarketInfo{Name: f.Name, Owner: f.Owner.Name, Description: firstNonEmpty(f.Description, f.Metadata.Description), Plugins: []MarketPlugin{}}
	entries := map[string]*marketEntry{}
	pluginRoot := strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(f.Metadata.PluginRoot), "./"), "/")
	if pluginRoot != "" {
		// pluginRoot 本身必须是 marketplace 内的相对路径：否则它能把“裸名”指到仓库外。
		if _, err := insideDir(root, pluginRoot); err != nil {
			return nil, nil, fmt.Errorf("metadata.pluginRoot %q：%v", f.Metadata.PluginRoot, err)
		}
	}
	for _, rawEntry := range f.Plugins {
		if len(info.Plugins) >= maxMarketEntries {
			break
		}
		var e marketEntryJSON
		if json.Unmarshal(rawEntry, &e) != nil || !marketNameRe.MatchString(e.Name) || entries[e.Name] != nil {
			continue // 官方也是各条目各自校验、一个坏条目不影响整个 marketplace；重名的取第一个
		}
		me := &marketEntry{MarketPlugin: MarketPlugin{Name: e.Name, Description: truncRunes(e.Description, 300), Category: truncRunes(e.Category, 40)}}
		me.classify(root, pluginRoot, e.Source)
		entries[e.Name] = me
		info.Plugins = append(info.Plugins, me.MarketPlugin)
	}
	if len(info.Plugins) == 0 {
		return nil, nil, errors.New("这个 marketplace 里没有可用的插件条目")
	}
	return info, entries, nil
}

func truncRunes(s string, n int) string {
	if r := []rune(strings.TrimSpace(s)); len(r) > n {
		return string(r[:n])
	}
	return strings.TrimSpace(s)
}

// classify 判断条目的来源类型，校验它，能取就记下取的办法，不能取就写明原因。
func (e *marketEntry) classify(root, pluginRoot string, raw json.RawMessage) {
	unsupported := func(kind, where, reason string) {
		e.Kind, e.Where, e.Supported, e.Reason = kind, where, false, reason
	}
	var rel string
	if json.Unmarshal(raw, &rel) == nil && len(raw) > 0 && raw[0] == '"' {
		e.Kind = srcRelative
		switch {
		case rel == "." || rel == "./":
			e.rel = "."
		case strings.HasPrefix(rel, "./"):
			e.rel = strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(rel), "./"), "/")
		case pluginRoot != "" && !strings.Contains(rel, "/") && !strings.Contains(rel, `\`):
			e.rel = pluginRoot + "/" + rel // metadata.pluginRoot 下的裸名
		default:
			unsupported(srcRelative, rel, `相对路径必须以 "./" 开头`)
			return
		}
		e.Where = e.rel
		if e.rel != "." {
			if _, err := insideDir(root, e.rel); err != nil {
				unsupported(srcRelative, rel, fmt.Sprintf("路径不合法：%v", err))
				return
			}
		}
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(e.rel))); err != nil || !fi.IsDir() {
			unsupported(srcRelative, e.rel, "这个目录不存在")
			return
		}
		e.Supported = true
		return
	}
	var o struct {
		Source  string `json:"source"`
		Repo    string `json:"repo"`
		URL     string `json:"url"`
		Ref     string `json:"ref"`
		SHA     string `json:"sha"`
		Path    string `json:"path"`
		Package string `json:"package"`
	}
	if json.Unmarshal(raw, &o) != nil || o.Source == "" {
		unsupported("", "", "来源写法不被支持")
		return
	}
	e.ref, e.sha = o.Ref, o.SHA
	switch o.Source {
	case srcGitHub:
		if !repoRe.MatchString(o.Repo) {
			unsupported(srcGitHub, o.Repo, "repo 要写成 owner/repo")
			return
		}
		e.Kind, e.Where, e.gitURL = srcGitHub, o.Repo, "https://github.com/"+o.Repo
	case srcURL:
		if !strings.HasPrefix(strings.ToLower(o.URL), "https://") {
			unsupported(srcURL, o.URL, "只支持 https:// 开头的 Git 地址（不支持 http、ssh、file）")
			return
		}
		e.Kind, e.Where, e.gitURL = srcURL, o.URL, o.URL
	case srcSubdir:
		u := o.URL
		if repoRe.MatchString(u) {
			u = "https://github.com/" + u // owner/repo 简写
		}
		if !strings.HasPrefix(strings.ToLower(u), "https://") {
			unsupported(srcSubdir, o.URL, "只支持 https:// 开头的 Git 地址或 owner/repo 简写")
			return
		}
		if o.Path == "" {
			unsupported(srcSubdir, o.URL, "缺少 path")
			return
		}
		e.Kind, e.Where, e.gitURL, e.path = srcSubdir, o.URL+" › "+o.Path, u, o.Path
	case srcNPM:
		unsupported(srcNPM, o.Package, "npm 包来源暂不支持")
		return
	case srcArchive:
		unsupported(srcArchive, o.URL, "压缩包来源暂不支持（请改用 Git 地址，或下载后用“安装 zip”）")
		return
	case srcCommand:
		unsupported(srcCommand, "", "会在本机运行命令来生成插件，Mosaic 不支持这种来源")
		return
	default:
		unsupported(o.Source, "", fmt.Sprintf("不认识的来源类型 %q", o.Source))
		return
	}
	// 提前校验，选的时候才不会因为地址不合法才失败：地址、分支、子目录、sha。
	if err := validateGit(e.gitURL, e.ref, e.path); err != nil {
		e.Supported, e.Reason = false, err.Error()
		return
	}
	if e.sha != "" && !shaRe.MatchString(e.sha) {
		e.Supported, e.Reason = false, "sha 必须是 40 位小写十六进制的提交号"
		return
	}
	e.Supported = true
}

// InspectSource 看一眼用户给的来源是什么：marketplace 就列出里面的插件；否则当作一个插件，照常预览。
// 来源可以是文件夹、.zip、https 的 Git 地址（末尾 #分支或标签）。
func (h *Host) InspectSource(src string) (SourceInspection, error) {
	src = strings.TrimSpace(src)
	if isGitSource(src) {
		return h.inspectGit(src)
	}
	if fi, err := os.Stat(src); err == nil && fi.IsDir() && hasMarketplace(src) {
		// 本地文件夹形式的 marketplace：直接读，不用复制。
		return h.registerMarket(src, false, src)
	}
	p, err := h.Preview(src)
	if err != nil {
		return SourceInspection{}, err
	}
	return SourceInspection{Kind: "plugin", Preview: &p}, nil
}

func (h *Host) inspectGit(src string) (SourceInspection, error) {
	rawURL, ref := splitRef(src)
	if err := validateGit(rawURL, ref, ""); err != nil {
		return SourceInspection{}, err
	}
	token, err := randomToken()
	if err != nil {
		return SourceInspection{}, err
	}
	clone := filepath.Join(h.opts.Dir, ".staging", token+".market")
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		return SourceInspection{}, err
	}
	if err := h.cloneGit(rawURL, ref, "", clone); err != nil {
		_ = os.RemoveAll(clone)
		return SourceInspection{}, err
	}
	if hasMarketplace(clone) {
		out, err := h.registerMarket(clone, true, src)
		if err != nil {
			_ = os.RemoveAll(clone)
		}
		return out, err
	}
	// 不是 marketplace：就是一个插件。已经克隆好了，直接从克隆里预览，不再下载一遍。
	defer os.RemoveAll(clone)
	p, err := h.stageNamed(src, hintFromSource(src), func(stage string) error { return copyDir(clone, stage) })
	if err != nil {
		return SourceInspection{}, err
	}
	return SourceInspection{Kind: "plugin", Preview: &p}, nil
}

func (h *Host) registerMarket(root string, owned bool, label string) (SourceInspection, error) {
	info, entries, err := parseMarketplace(root)
	if err != nil {
		return SourceInspection{}, err
	}
	token, err := randomToken()
	if err != nil {
		return SourceInspection{}, err
	}
	info.Token, info.Source = token, label
	h.mu.Lock()
	h.markets[token] = &stagedMarket{root: root, owned: owned, label: label, entries: entries, at: time.Now()}
	h.mu.Unlock()
	return SourceInspection{Kind: "marketplace", Market: info}, nil
}

// PreviewMarketplacePlugin 取 marketplace 里的一个插件，返回安装前要给用户确认的内容（之后和其他来源的流程相同）。
func (h *Host) PreviewMarketplacePlugin(token, name string) (Preview, error) {
	h.mu.Lock()
	m := h.markets[token]
	h.mu.Unlock()
	if m == nil {
		return Preview{}, errors.New("这次选择已经失效，请重新输入来源")
	}
	e := m.entries[name]
	if e == nil {
		return Preview{}, fmt.Errorf("marketplace 里没有插件 %q", name)
	}
	if !e.Supported {
		return Preview{}, fmt.Errorf("插件 %q 暂时不能安装：%s", name, e.Reason)
	}
	if e.Kind == srcRelative {
		src, err := insideDir(m.root, e.rel)
		if e.rel == "." {
			src, err = m.root, nil
		}
		if err != nil {
			return Preview{}, err
		}
		return h.stageNamed(m.label+" › "+name, name, func(stage string) error { return copyDir(src, stage) })
	}
	label := e.gitURL
	if e.ref != "" {
		label += "#" + e.ref
	}
	return h.previewGit(gitRequest{url: e.gitURL, ref: e.ref, subdir: e.path, sha: e.sha, hint: name, label: label + "（来自 " + m.label + "）"})
}

// CancelMarketplace 放弃一次选择，清理临时的克隆。
func (h *Host) CancelMarketplace(token string) {
	h.mu.Lock()
	m := h.markets[token]
	delete(h.markets, token)
	h.mu.Unlock()
	if m != nil && m.owned {
		_ = os.RemoveAll(m.root)
	}
}
