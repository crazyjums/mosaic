package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/secret"
	"github.com/crazyjums/myllmapp/internal/skill"
)

// MCPApplier 是 Host 需要的 MCP 管理能力（*mcp.Manager 实现了它，测试里可以替换）。
type MCPApplier interface {
	ApplySource(source string, cfgs []mcp.ServerConfig) error
}

// HostOptions 是 Host 的依赖。
type HostOptions struct {
	// Dir 是插件代码目录（安装后只读）；DataDir 是插件运行时数据的根目录（每个插件一个子目录）；
	// StateFile 是安装状态文件（启用与否、配置、安装确认的结果）。
	Dir, DataDir, StateFile string
	// Secrets 保存 secret 类型的配置（系统钥匙串）。为 nil 时 secret 配置不可用。
	Secrets secret.Store
	MCP     MCPApplier
	// AppVersion 用于检查 min_app_version；"dev" 或空表示不检查。
	AppVersion string
	// Catalog 是随应用发布的目录（结构见 catalog 包：registry.json 和 bundled/<目录>）；为 nil 时市场里只有远程列表。
	Catalog fs.FS
	// RegistryURL 是在线插件列表的地址；为空时不联网刷新，只用内置列表。
	RegistryURL string
	// LookPath 查找外部命令（检查 requires），默认 exec.LookPath；测试里可以替换。
	LookPath func(string) (string, error)
}

// Host 管理所有插件：安装、启停、配置，并把它们带来的东西分发给技能目录、斜杠命令菜单和 MCP 管理器。
// 方法都可以并发调用。
type Host struct {
	opts HostOptions

	mu      sync.Mutex
	state   stateFile
	plugins map[string]*loaded
	staged  map[string]*stagedPlugin
	warn    map[string]string
	// remote 是在线列表里（上次刷新的）条目，见 catalog.go。
	remote []registryEntry
}

type stateFile struct {
	Version int                     `json:"version"`
	Plugins map[string]*pluginState `json:"plugins"`
}

// pluginState 是一个插件持久化的状态。
type pluginState struct {
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config,omitempty"`
	// Secrets 记录哪些 secret 配置项在钥匙串里有值（界面据此显示“已设置”，不需要去读钥匙串）。
	Secrets []string `json:"secrets,omitempty"`
	// ReadApproved 是用户在安装确认时同意按作者声明的风险等级运行的 MCP 服务名；没在里面的一律按 exec。
	ReadApproved []string  `json:"read_approved,omitempty"`
	Source       string    `json:"source,omitempty"`
	InstalledAt  time.Time `json:"installed_at"`
}

type loaded struct {
	dir string
	m   *Manifest
	// err 是加载失败或不兼容的原因；非空时这个插件不会被启用。
	err string
}

type stagedPlugin struct {
	dir    string
	m      *Manifest
	source string
	at     time.Time
}

const stateVersion = 1

// NewHost 创建 Host：确保目录存在，清理上次遗留的临时目录，读取状态并扫描已安装的插件，
// 然后让已启用的插件的 MCP 服务生效。单个插件加载失败只影响它自己。
func NewHost(opts HostOptions) (*Host, error) {
	if opts.Dir == "" || opts.StateFile == "" {
		return nil, errors.New("plugin: Dir and StateFile are required")
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	for _, d := range []string{opts.Dir, opts.DataDir} {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("plugin: %w", err)
		}
	}
	h := &Host{opts: opts, plugins: map[string]*loaded{}, staged: map[string]*stagedPlugin{}, warn: map[string]string{}}
	h.cleanLeftovers()
	h.loadState()
	h.loadRegistryCache()
	h.scan()
	h.mu.Lock()
	h.applyLocked()
	h.mu.Unlock()
	return h, nil
}

// cleanLeftovers 删掉上次没有完成的安装留下的临时目录。
func (h *Host) cleanLeftovers() {
	_ = os.RemoveAll(filepath.Join(h.opts.Dir, ".staging"))
	entries, _ := os.ReadDir(h.opts.Dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".old-") {
			_ = os.RemoveAll(filepath.Join(h.opts.Dir, e.Name()))
		}
	}
}

func (h *Host) loadState() {
	h.state = stateFile{Version: stateVersion, Plugins: map[string]*pluginState{}}
	data, err := os.ReadFile(h.opts.StateFile)
	if err != nil {
		return
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil || st.Plugins == nil {
		// 状态文件坏了：留一份备份，从空状态开始（插件本身还在，只是需要重新启用和配置）。
		_ = os.Rename(h.opts.StateFile, h.opts.StateFile+".bad")
		return
	}
	h.state = st
}

func (h *Host) saveStateLocked() error {
	data, err := json.MarshalIndent(h.state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(h.opts.StateFile, data)
}

// writeFileAtomic 先写临时文件再改名，进程中途退出也不会留下写了一半的文件。
func writeFileAtomic(file string, data []byte) error {
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// scan 读取插件目录下所有插件的清单。
func (h *Host) scan() {
	entries, err := os.ReadDir(h.opts.Dir)
	if err != nil {
		return
	}
	h.plugins = map[string]*loaded{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(h.opts.Dir, e.Name())
		m, err := ParseManifest(dir, h.opts.AppVersion)
		l := &loaded{dir: dir, m: m}
		if err != nil {
			l.err = err.Error()
			var inc *IncompatibleError
			if m == nil || !errors.As(err, &inc) {
				// 清单本身有问题（不是只因为版本不兼容）：只能用目录名当 id 显示。
				l.m = &Manifest{ID: e.Name(), Name: e.Name()}
			}
		} else if m.ID != e.Name() {
			l.err = fmt.Sprintf("目录名 %q 与清单里的 id %q 不一致", e.Name(), m.ID)
		}
		h.plugins[l.m.ID] = l
	}
}

// ---- 列表与详情 ----

// Info 是插件的概要，界面的插件页用它。
type Info struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	// Error 是不能加载的原因（清单有问题、需要升级应用）；Warning 是运行中的提示（缺必填配置等）。
	Error   string `json:"error,omitempty"`
	Warning string `json:"warning,omitempty"`
	Source  string `json:"source,omitempty"`

	Skills      []string      `json:"skills"`
	Commands    []CommandView `json:"commands"`
	Servers     []ServerView  `json:"servers"`
	Config      []ConfigView  `json:"config"`
	HasStatus   bool          `json:"has_status"`
	Actions     []ActionView  `json:"actions"`
	Requires    []Requirement `json:"requires"`
	Network     []string      `json:"network"`
	InstalledAt time.Time     `json:"installed_at,omitempty"`
}

// CommandView 是一个斜杠命令的概要。
type CommandView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ServerView 是一个 MCP 服务的概要。
type ServerView struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	// Command 是用来显示的命令行（安装确认时让用户看清要启动什么）。
	Command string `json:"command"`
	// Declared 是作者声明的风险等级；Effective 是实际生效的（没经用户确认时一律是 exec）。
	Declared  string `json:"declared_risk"`
	Effective string `json:"effective_risk"`
	Approved  bool   `json:"approved"`
	// Sensitive：工具会读取私密内容，每次调用都询问。
	Sensitive bool `json:"sensitive,omitempty"`
}

// ConfigView 是设置表单的一项加上当前值。secret 的值永远不返回，只说有没有设置。
type ConfigView struct {
	ConfigField
	Value     string `json:"value"`
	HasSecret bool   `json:"has_secret"`
}

// ActionView 是一个操作按钮。
type ActionView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Requirement 是一项依赖检查的结果。
type Requirement struct {
	Command string `json:"command"`
	Found   bool   `json:"found"`
}

// List 返回所有已安装的插件，按名字排序。
func (h *Host) List() []Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Info, 0, len(h.plugins))
	for id := range h.plugins {
		out = append(out, h.infoLocked(id))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// Get 返回一个插件的详情。
func (h *Host) Get(id string) (Info, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.plugins[id]; !ok {
		return Info{}, fmt.Errorf("没有安装插件 %q", id)
	}
	return h.infoLocked(id), nil
}

func (h *Host) infoLocked(id string) Info {
	l := h.plugins[id]
	st := h.state.Plugins[id]
	return h.describe(l.m, l.dir, st, l.err, h.warn[id])
}

// describe 把清单和状态整理成界面用的 Info。st 可以为 nil（还没安装的预览）。
func (h *Host) describe(m *Manifest, dir string, st *pluginState, errText, warn string) Info {
	info := Info{
		ID: m.ID, Name: m.Name, Version: m.Version, Description: m.Description,
		Error: errText, Warning: warn,
		Skills: []string{}, Commands: []CommandView{}, Servers: []ServerView{}, Config: []ConfigView{},
		Actions: []ActionView{}, Requires: []Requirement{}, Network: append([]string{}, m.Network...),
		HasStatus: m.Status != nil,
	}
	if st != nil {
		info.Enabled, info.Source, info.InstalledAt = st.Enabled, st.Source, st.InstalledAt
	}
	for _, s := range m.Skills {
		info.Skills = append(info.Skills, filepath.Base(filepath.FromSlash(s.Path)))
	}
	for _, c := range m.Commands {
		info.Commands = append(info.Commands, CommandView{Name: c.Name, Description: c.Description})
	}
	for _, s := range m.MCP {
		name := m.ServerName(s)
		approved := st != nil && contains(st.ReadApproved, name)
		declared := s.Risk
		if declared == "" {
			declared = permission.RiskExec
		}
		eff := permission.RiskExec
		if approved {
			eff = declared
		}
		transport := s.Transport
		if transport == "" {
			transport = mcp.TransportStdio
		}
		cmdline := strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
		if transport == mcp.TransportHTTP {
			cmdline = s.URL
		}
		info.Servers = append(info.Servers, ServerView{Name: name, Transport: transport, Command: cmdline, Declared: string(declared), Effective: string(eff), Approved: approved, Sensitive: s.Sensitive})
	}
	for _, c := range m.Config {
		v := ConfigView{ConfigField: c}
		if c.Type == TypeSecret {
			v.HasSecret = st != nil && contains(st.Secrets, c.Key)
		} else if st != nil {
			if val, ok := st.Config[c.Key]; ok {
				v.Value = val
			} else {
				v.Value = c.Default
			}
		} else {
			v.Value = c.Default
		}
		info.Config = append(info.Config, v)
	}
	for _, a := range m.Actions {
		info.Actions = append(info.Actions, ActionView{ID: a.ID, Label: a.Label})
	}
	for _, c := range m.Requires.Commands {
		_, err := h.opts.LookPath(c)
		info.Requires = append(info.Requires, Requirement{Command: c, Found: err == nil})
	}
	return info
}

// ---- 安装 ----

// Preview 是安装前给用户确认的内容。
type Preview struct {
	// Token 在 Install / CancelPreview 时带回来。
	Token  string `json:"token"`
	Plugin Info   `json:"plugin"`
	// Existing 不为空表示已经装了同 id 的插件，安装会覆盖更新（保留配置，重新确认风险）。
	Existing *Info `json:"existing,omitempty"`
}

// Preview 校验 src（插件目录或 .zip 文件）并复制到临时目录，返回安装前要给用户确认的内容。
// 此时什么都没有生效；确认后调用 Install，放弃调用 CancelPreview（不调用也会在下次启动时清理）。
func (h *Host) Preview(src string) (Preview, error) {
	src = strings.TrimSpace(src)
	if isGitSource(src) {
		url, ref := splitRef(src)
		return h.PreviewGit(url, ref, "")
	}
	fi, err := os.Stat(src)
	if err != nil {
		return Preview{}, fmt.Errorf("找不到 %s", src)
	}
	return h.stage(src, func(stage string) error {
		switch {
		case fi.IsDir():
			return copyDir(src, stage)
		case strings.EqualFold(filepath.Ext(src), ".zip"):
			return extractZip(src, stage)
		}
		return errors.New("请选择插件目录或 .zip 文件")
	})
}

// stage 把一个插件放进临时目录并校验清单，登记为待确认的安装。fill 负责把内容放进（已存在的空）临时目录；
// source 只用来记录来源。无论来源是目录、zip、Git 还是内置，之后的校验、确认和安装走同一条路。
func (h *Host) stage(source string, fill func(stage string) error) (Preview, error) {
	token, err := randomToken()
	if err != nil {
		return Preview{}, err
	}
	stage := filepath.Join(h.opts.Dir, ".staging", token)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return Preview{}, err
	}
	cleanup := func() { _ = os.RemoveAll(stage) }
	if err := fill(stage); err != nil {
		cleanup()
		return Preview{}, err
	}
	root, err := pluginRoot(stage)
	if err != nil {
		cleanup()
		return Preview{}, err
	}
	m, err := ParseManifest(root, h.opts.AppVersion)
	if err != nil {
		cleanup()
		return Preview{}, err
	}
	// 压缩包里的内容在子目录里时，把它提到临时目录的最外层，之后统一按“临时目录就是插件目录”处理。
	if root != stage {
		flat := stage + ".flat"
		if err := os.Rename(root, flat); err != nil {
			cleanup()
			return Preview{}, err
		}
		_ = os.RemoveAll(stage)
		if err := os.Rename(flat, stage); err != nil {
			return Preview{}, err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.staged[token] = &stagedPlugin{dir: stage, m: m, source: source, at: time.Now()}
	p := Preview{Token: token, Plugin: h.describe(m, stage, nil, "", "")}
	if _, ok := h.plugins[m.ID]; ok {
		ex := h.infoLocked(m.ID)
		p.Existing = &ex
	}
	return p, nil
}

// CancelPreview 放弃一次安装，清理临时目录。
func (h *Host) CancelPreview(token string) {
	h.mu.Lock()
	sp := h.staged[token]
	delete(h.staged, token)
	h.mu.Unlock()
	if sp != nil {
		_ = os.RemoveAll(sp.dir)
	}
}

// Install 安装 Preview 过的插件并启用。approved 是用户在确认窗口里勾选的、同意按作者声明的风险等级运行的
// MCP 服务名（没勾选的一律按 exec）。同 id 已安装时覆盖更新，保留配置，风险确认以这次的为准。
func (h *Host) Install(token string, approved []string) (Info, error) {
	h.mu.Lock()
	sp := h.staged[token]
	h.mu.Unlock()
	if sp == nil {
		return Info{}, errors.New("这次安装已经失效，请重新选择插件")
	}
	declared := map[string]bool{}
	for _, s := range sp.m.MCP {
		declared[sp.m.ServerName(s)] = true
	}
	for _, a := range approved {
		if !declared[a] {
			return Info{}, fmt.Errorf("%q 不是这个插件声明的 MCP 服务", a)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.staged, token)
	id := sp.m.ID
	final := filepath.Join(h.opts.Dir, id)
	old := ""
	if _, err := os.Stat(final); err == nil {
		old = filepath.Join(h.opts.Dir, ".old-"+id+"-"+time.Now().Format("150405.000"))
		if err := os.Rename(final, old); err != nil {
			_ = os.RemoveAll(sp.dir)
			return Info{}, fmt.Errorf("无法替换已安装的插件：%v", err)
		}
	}
	if err := os.Rename(sp.dir, final); err != nil {
		if old != "" {
			_ = os.Rename(old, final)
		}
		_ = os.RemoveAll(sp.dir)
		return Info{}, fmt.Errorf("安装失败：%v", err)
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	st := h.state.Plugins[id]
	if st == nil {
		st = &pluginState{Config: map[string]string{}}
		h.state.Plugins[id] = st
	}
	st.Enabled, st.Source, st.InstalledAt = true, sp.source, time.Now()
	st.ReadApproved = append([]string(nil), approved...)
	if err := h.saveStateLocked(); err != nil {
		return Info{}, err
	}
	h.scan()
	h.applyLocked()
	return h.infoLocked(id), nil
}

// ---- 启停、卸载、配置 ----

// SetEnabled 启用或停用插件。停用时摘掉它的技能、命令和 MCP 工具（子进程随之停止），配置和密钥保留。
func (h *Host) SetEnabled(id string, on bool) (Info, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.plugins[id]
	if !ok {
		return Info{}, fmt.Errorf("没有安装插件 %q", id)
	}
	if on && l.err != "" {
		return Info{}, fmt.Errorf("这个插件现在不能启用：%s", l.err)
	}
	st := h.stateLocked(id)
	st.Enabled = on
	if err := h.saveStateLocked(); err != nil {
		return Info{}, err
	}
	h.applyLocked()
	return h.infoLocked(id), nil
}

func (h *Host) stateLocked(id string) *pluginState {
	st := h.state.Plugins[id]
	if st == nil {
		st = &pluginState{Config: map[string]string{}}
		h.state.Plugins[id] = st
	}
	return st
}

// Uninstall 卸载插件。purge 为 true 时一并清除配置、钥匙串里的密钥和插件的运行时数据。
func (h *Host) Uninstall(id string, purge bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.plugins[id]
	if !ok {
		return fmt.Errorf("没有安装插件 %q", id)
	}
	st := h.state.Plugins[id]
	if st != nil {
		st.Enabled = false
	}
	h.applyLocked() // 先停掉它的 MCP 子进程，再删文件
	delete(h.plugins, id)
	if err := os.RemoveAll(l.dir); err != nil {
		return err
	}
	if purge {
		if h.opts.Secrets != nil && st != nil {
			for _, k := range st.Secrets {
				_ = h.opts.Secrets.Delete(secretKey(id, k))
			}
		}
		delete(h.state.Plugins, id)
		_ = os.RemoveAll(h.dataDir(id))
	}
	return h.saveStateLocked()
}

func secretKey(id, key string) string { return "plugin/" + id + "/" + key }

func (h *Host) dataDir(id string) string {
	if h.opts.DataDir == "" {
		return ""
	}
	return filepath.Join(h.opts.DataDir, id)
}

// SetConfig 保存配置表单。values 里 secret 类型的项为空串表示“不修改”（界面不回显密钥）；清除用 ClearSecret。
// 保存后重新应用：配置变了的 MCP 服务会用新的值重启。
func (h *Host) SetConfig(id string, values map[string]string) (Info, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.plugins[id]
	if !ok {
		return Info{}, fmt.Errorf("没有安装插件 %q", id)
	}
	fields := map[string]ConfigField{}
	for _, c := range l.m.Config {
		fields[c.Key] = c
	}
	for k, v := range values {
		c, ok := fields[k]
		if !ok {
			return Info{}, fmt.Errorf("插件没有配置项 %q", k)
		}
		if len(v) > 4096 {
			return Info{}, fmt.Errorf("%s 的值太长", c.Label)
		}
		switch c.Type {
		case TypeBool:
			if v != "true" && v != "false" {
				return Info{}, fmt.Errorf("%s 的值必须是 true 或 false", c.Label)
			}
		case TypeSelect:
			if !contains(c.Options, v) {
				return Info{}, fmt.Errorf("%s 的值 %q 不在可选项里", c.Label, v)
			}
		}
	}
	st := h.stateLocked(id)
	if st.Config == nil {
		st.Config = map[string]string{}
	}
	for k, v := range values {
		c := fields[k]
		if c.Type != TypeSecret {
			st.Config[k] = v
			continue
		}
		if v == "" {
			continue
		}
		if h.opts.Secrets == nil {
			return Info{}, errors.New("系统钥匙串不可用，不能保存密钥")
		}
		if err := h.opts.Secrets.Set(secretKey(id, k), v); err != nil {
			return Info{}, fmt.Errorf("保存密钥失败：%v", err)
		}
		if !contains(st.Secrets, k) {
			st.Secrets = append(st.Secrets, k)
		}
	}
	if err := h.saveStateLocked(); err != nil {
		return Info{}, err
	}
	h.applyLocked()
	return h.infoLocked(id), nil
}

// ClearSecret 清除一个 secret 配置项（从钥匙串删除）。
func (h *Host) ClearSecret(id, key string) (Info, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.plugins[id]; !ok {
		return Info{}, fmt.Errorf("没有安装插件 %q", id)
	}
	st := h.stateLocked(id)
	if h.opts.Secrets != nil {
		if err := h.opts.Secrets.Delete(secretKey(id, key)); err != nil && !errors.Is(err, secret.ErrNotFound) {
			return Info{}, err
		}
	}
	kept := st.Secrets[:0]
	for _, k := range st.Secrets {
		if k != key {
			kept = append(kept, k)
		}
	}
	st.Secrets = kept
	if err := h.saveStateLocked(); err != nil {
		return Info{}, err
	}
	h.applyLocked()
	return h.infoLocked(id), nil
}

// ---- 分发给各个注册表 ----

// SkillFolders 返回已启用插件提供的技能文件夹。read_skill 和提示词每次都重新取，启停立即生效。
func (h *Host) SkillFolders() []skill.Folder {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []skill.Folder
	for _, id := range h.activeIDsLocked() {
		l := h.plugins[id]
		for _, s := range l.m.Skills {
			if p, err := insideDir(l.dir, s.Path); err == nil {
				out = append(out, skill.Folder{Dir: p, Source: "plugin:" + id, Origin: l.m.Name})
			}
		}
	}
	return out
}

// activeIDsLocked 返回已启用且没有加载错误的插件 id（有序）。
func (h *Host) activeIDsLocked() []string {
	var ids []string
	for id, l := range h.plugins {
		if st := h.state.Plugins[id]; st != nil && st.Enabled && l.err == "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// applyLocked 让各个注册表与当前状态一致：目前只有 MCP 需要主动推送（技能和命令是每次使用时现取的）。
func (h *Host) applyLocked() {
	h.warn = map[string]string{}
	if h.opts.MCP == nil {
		return
	}
	active := map[string]bool{}
	for _, id := range h.activeIDsLocked() {
		active[id] = true
	}
	ids := map[string]bool{}
	for id := range h.plugins {
		ids[id] = true
	}
	for id := range h.state.Plugins { // 已卸载但状态还在的：确保它的服务也被摘掉
		ids[id] = true
	}
	for id := range ids {
		var cfgs []mcp.ServerConfig
		if active[id] {
			var err error
			if cfgs, err = h.serverConfigsLocked(id); err != nil {
				h.warn[id] = err.Error()
				cfgs = nil
			}
		}
		if err := h.opts.MCP.ApplySource("plugin:"+id, cfgs); err != nil {
			h.warn[id] = err.Error()
		}
	}
}

// serverConfigsLocked 把插件声明的 MCP 服务展开成 mcp.ServerConfig：展开变量、读出密钥、套用确认过的风险等级。
func (h *Host) serverConfigsLocked(id string) ([]mcp.ServerConfig, error) {
	l := h.plugins[id]
	if len(l.m.MCP) == 0 {
		return nil, nil
	}
	vars, err := h.varsLocked(id)
	if err != nil {
		return nil, err
	}
	st := h.state.Plugins[id]
	var out []mcp.ServerConfig
	for _, s := range l.m.MCP {
		name := l.m.ServerName(s)
		cfg := mcp.ServerConfig{Name: name, Transport: s.Transport, Risk: permission.RiskExec, Sensitive: s.Sensitive}
		if cfg.Transport == "" {
			cfg.Transport = mcp.TransportStdio
		}
		if st != nil && contains(st.ReadApproved, name) && s.Risk != "" {
			cfg.Risk = s.Risk
		}
		if cfg.Command, err = expand(s.Command, vars); err != nil {
			return nil, err
		}
		for _, a := range s.Args {
			v, err := expand(a, vars)
			if err != nil {
				return nil, err
			}
			cfg.Args = append(cfg.Args, v)
		}
		if len(s.Env) > 0 {
			cfg.Env = map[string]string{}
			for k, v := range s.Env {
				if cfg.Env[k], err = expand(v, vars); err != nil {
					return nil, err
				}
			}
		}
		// 子进程的工作目录默认是插件目录；声明了 cwd 就用声明的（展开变量后必须仍在插件目录或数据目录内）。
		cfg.Cwd = l.dir
		if s.Cwd != "" {
			cwd, err := expand(s.Cwd, vars)
			if err != nil {
				return nil, err
			}
			if !filepath.IsAbs(cwd) {
				cwd = filepath.Join(l.dir, filepath.FromSlash(cwd))
			}
			if !within(cwd, l.dir) && !within(cwd, h.dataDir(id)) {
				return nil, fmt.Errorf("mcp 的 cwd %q 不在插件目录或数据目录内", s.Cwd)
			}
			cfg.Cwd = cwd
		}
		cfg.URL, err = expand(s.URL, vars)
		if err != nil {
			return nil, err
		}
		if len(s.Headers) > 0 {
			cfg.Headers = map[string]string{}
			for k, v := range s.Headers {
				if cfg.Headers[k], err = expand(v, vars); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, cfg)
	}
	return out, nil
}

func within(p, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// varsLocked 返回展开 ${...} 用的变量表。必填的配置项没填、或 secret 读不出来时报错：
// 这时服务不会启动，界面上显示原因（比起带着空密钥启动再失败，提示更清楚）。
func (h *Host) varsLocked(id string) (map[string]string, error) {
	l := h.plugins[id]
	st := h.state.Plugins[id]
	vars := map[string]string{"PLUGIN_DIR": l.dir, "DATA_DIR": h.dataDir(id)}
	if d := h.dataDir(id); d != "" {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	for _, c := range l.m.Config {
		var v string
		switch {
		case c.Type == TypeSecret:
			if st != nil && contains(st.Secrets, c.Key) && h.opts.Secrets != nil {
				got, err := h.opts.Secrets.Get(secretKey(id, c.Key))
				if err != nil {
					return nil, fmt.Errorf("读取密钥 %s 失败：%v", c.Label, err)
				}
				v = got
			}
		case st != nil && st.Config != nil:
			if got, ok := st.Config[c.Key]; ok {
				v = got
			} else {
				v = c.Default
			}
		default:
			v = c.Default
		}
		if v == "" && c.Required {
			return nil, fmt.Errorf("还没有填写必填项「%s」", c.Label)
		}
		vars["config."+c.Key] = v
	}
	return vars, nil
}

// ---- 斜杠命令 ----

// CommandInfo 是输入框 / 菜单里的一个插件命令。
type CommandInfo struct {
	// Name 是 / 后面输入的名字：不重名时是命令名，不同插件重名时是 "<插件id>:<命令名>"。
	Name        string `json:"name"`
	Description string `json:"description"`
	PluginID    string `json:"plugin_id"`
	PluginName  string `json:"plugin_name"`
}

type commandEntry struct {
	info CommandInfo
	path string
	raw  string // 清单里的命令名
}

func (h *Host) commandsLocked() []commandEntry {
	var all []commandEntry
	count := map[string]int{}
	for _, id := range h.activeIDsLocked() {
		l := h.plugins[id]
		for _, c := range l.m.Commands {
			p, err := insideDir(l.dir, c.Path)
			if err != nil {
				continue
			}
			desc := c.Description
			if t, err := loadCommandTemplate(p); err == nil && desc == "" {
				desc = t.description
			}
			all = append(all, commandEntry{info: CommandInfo{Name: c.Name, Description: desc, PluginID: id, PluginName: l.m.Name}, path: p, raw: c.Name})
			count[c.Name]++
		}
	}
	for i := range all {
		if count[all[i].raw] > 1 {
			all[i].info.Name = all[i].info.PluginID + ":" + all[i].raw
		}
	}
	return all
}

// Commands 返回已启用插件提供的斜杠命令。
func (h *Host) Commands() []CommandInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []CommandInfo{}
	for _, e := range h.commandsLocked() {
		out = append(out, e.info)
	}
	return out
}

// ExpandCommand 把 "/name args" 里的命令展开成要发给模型的文本。名字不区分大小写；
// 重名的命令可以用 "<插件id>:<名字>" 指定。找不到命令时返回 ok=false（调用方照常把原文发出去）。
func (h *Host) ExpandCommand(name, args string) (text string, ok bool, err error) {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	h.mu.Lock()
	var path string
	for _, e := range h.commandsLocked() {
		if strings.EqualFold(e.info.Name, name) || e.info.PluginID+":"+e.raw == name {
			path = e.path
			break
		}
	}
	h.mu.Unlock()
	if path == "" {
		return "", false, nil
	}
	t, err := loadCommandTemplate(path)
	if err != nil {
		return "", true, err
	}
	return t.render(args), true, nil
}

// ---- 状态检查与操作 ----

// 状态检查的结果状态。
const (
	StateOK                = "ok"
	StateBad               = "bad"
	StateMissingDependency = "missing_dependency"
	StateNone              = "none"
	StateUnknown           = "unknown"
)

// StatusResult 是一次状态检查的结果。
type StatusResult struct {
	State   string   `json:"state"`
	Message string   `json:"message,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

// ActionResult 是一次操作的结果。
type ActionResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// missing 返回缺少的依赖命令。
func (h *Host) missing(m *Manifest) []string {
	var out []string
	for _, c := range m.Requires.Commands {
		if _, err := h.opts.LookPath(c); err != nil {
			out = append(out, c)
		}
	}
	return out
}

// prepareRun 展开一条状态 / 操作命令，返回要运行的参数、插件目录和额外环境变量。
func (h *Host) prepareRun(id, command string) (argv []string, dir string, env []string, m *Manifest, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.plugins[id]
	if !ok {
		return nil, "", nil, nil, fmt.Errorf("没有安装插件 %q", id)
	}
	if l.err != "" {
		return nil, "", nil, nil, fmt.Errorf("这个插件现在不能运行：%s", l.err)
	}
	vars, err := h.varsLocked(id)
	if err != nil {
		return nil, "", nil, nil, err
	}
	argv, err = expandArgs(command, vars)
	if err != nil {
		return nil, "", nil, nil, err
	}
	env = []string{"PLUGIN_DIR=" + l.dir, "DATA_DIR=" + vars["DATA_DIR"]}
	return argv, l.dir, env, l.m, nil
}

// Status 运行插件声明的状态检查。依赖缺失时不运行命令，直接报告缺什么。
func (h *Host) Status(ctx context.Context, id string) (StatusResult, error) {
	h.mu.Lock()
	l, ok := h.plugins[id]
	h.mu.Unlock()
	if !ok {
		return StatusResult{}, fmt.Errorf("没有安装插件 %q", id)
	}
	if l.m.Status == nil {
		return StatusResult{State: StateNone}, nil
	}
	if miss := h.missing(l.m); len(miss) > 0 {
		return StatusResult{State: StateMissingDependency, Missing: miss, Message: "缺少依赖：" + strings.Join(miss, "、")}, nil
	}
	argv, dir, env, m, err := h.prepareRun(id, l.m.Status.Command)
	if err != nil {
		return StatusResult{State: StateUnknown, Message: err.Error()}, nil
	}
	res := runCommand(ctx, dir, argv, env, statusTimeout)
	if res.Err != nil {
		return StatusResult{State: StateUnknown, Message: res.Err.Error()}, nil
	}
	msg := strings.TrimSpace(res.Output)
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	if res.ExitCode != 0 {
		return StatusResult{State: StateBad, Message: msg}, nil
	}
	if m.Status.OKPath == "" {
		return StatusResult{State: StateOK, Message: msg}, nil
	}
	v, found := jsonPath([]byte(res.Output), m.Status.OKPath)
	switch {
	case !found:
		return StatusResult{State: StateUnknown, Message: "状态命令的输出里没有 " + m.Status.OKPath}, nil
	case truthy(v):
		return StatusResult{State: StateOK, Message: msg}, nil
	}
	return StatusResult{State: StateBad, Message: msg}, nil
}

// RunAction 运行插件声明的一个操作按钮，返回输出。
func (h *Host) RunAction(ctx context.Context, id, actionID string) (ActionResult, error) {
	h.mu.Lock()
	l, ok := h.plugins[id]
	h.mu.Unlock()
	if !ok {
		return ActionResult{}, fmt.Errorf("没有安装插件 %q", id)
	}
	var action *ActionSpec
	for i := range l.m.Actions {
		if l.m.Actions[i].ID == actionID {
			action = &l.m.Actions[i]
		}
	}
	if action == nil {
		return ActionResult{}, fmt.Errorf("插件没有操作 %q", actionID)
	}
	if miss := h.missing(l.m); len(miss) > 0 {
		return ActionResult{Error: "缺少依赖：" + strings.Join(miss, "、")}, nil
	}
	argv, dir, env, _, err := h.prepareRun(id, action.Command)
	if err != nil {
		return ActionResult{Error: err.Error()}, nil
	}
	res := runCommand(ctx, dir, argv, env, actionTimeout)
	out := ActionResult{Output: res.Output, ExitCode: res.ExitCode}
	if res.Err != nil {
		out.Error = res.Err.Error()
	}
	return out, nil
}

func randomToken() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
