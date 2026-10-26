package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/update"
)

// FileName 是清单文件名。
const FileName = "plugin.json"

// 清单的限制：插件是别人写的，清单里的数量和长度都要有上限。
const (
	maxManifestBytes = 256 << 10
	maxSkills        = 50
	maxCommands      = 50
	maxMCP           = 8
	maxConfig        = 30
	maxActions       = 10
	maxCommandBytes  = 64 << 10
)

var (
	idRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,23}$`)
	cmdNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)
	keyRe     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	reqNameRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9*]([A-Za-z0-9.*-]{0,251}[A-Za-z0-9*])?$`)
	varRe     = regexp.MustCompile(`\$\{([^}]*)\}`)
)

// Manifest 是 plugin.json 的内容，格式见 docs/design/plugin-system.md。
type Manifest struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Description   string `json:"description,omitempty"`
	MinAppVersion string `json:"min_app_version,omitempty"`
	Protocol      int    `json:"protocol,omitempty"`

	Skills   []SkillRef    `json:"skills,omitempty"`
	Commands []CommandRef  `json:"commands,omitempty"`
	MCP      []MCPSpec     `json:"mcp,omitempty"`
	Config   []ConfigField `json:"config,omitempty"`
	Status   *StatusSpec   `json:"status,omitempty"`
	Actions  []ActionSpec  `json:"actions,omitempty"`
	Requires Requires      `json:"requires,omitempty"`
	// Network 是声明会访问的域名：只用于安装确认，不做强制拦截。
	Network []string `json:"network,omitempty"`

	// 下面两项这个版本还不支持：清单里出现就拒绝加载并说明原因，而不是悄悄忽略。
	Hooks     json.RawMessage `json:"hooks,omitempty"`
	Subagents json.RawMessage `json:"subagents,omitempty"`
}

// SkillRef 指向一个技能文件夹（里面有 SKILL.md）。
type SkillRef struct {
	Path string `json:"path"`
}

// CommandRef 声明一个斜杠命令，模板是一个 markdown 文件。
type CommandRef struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

// MCPSpec 声明一个 MCP 服务。
type MCPSpec struct {
	// Name 为空或等于插件 id 时服务名就是插件 id，否则是 "<id>-<name>"。
	Name      string            `json:"name,omitempty"`
	Transport string            `json:"transport,omitempty"` // stdio（默认）/ http
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	// Risk 是作者声明的风险等级。只是“申请”：用户在安装确认里勾选之后才生效，否则一律按 exec（每次询问）。
	Risk permission.Risk `json:"risk,omitempty"`
}

// ConfigField 是设置表单里的一项。
type ConfigField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // string / secret / bool / select / path
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
	Help     string   `json:"help,omitempty"`
}

// 配置项类型。
const (
	TypeString = "string"
	TypeSecret = "secret"
	TypeBool   = "bool"
	TypeSelect = "select"
	TypePath   = "path"
)

// StatusSpec 声明一个状态检查：运行命令，输出 JSON，用 OKPath（点号路径）指向的字段判断是否正常；
// OKPath 为空时用退出码。
type StatusSpec struct {
	Command string `json:"command"`
	OKPath  string `json:"ok_path,omitempty"`
}

// ActionSpec 声明一个按钮：点击后运行命令，把输出显示出来。
type ActionSpec struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Command string `json:"command"`
}

// Requires 是插件依赖的外部条件。
type Requires struct {
	Commands []string `json:"commands,omitempty"`
}

// IncompatibleError 表示清单本身没问题，但这个版本的应用不能加载它（需要升级应用）。
type IncompatibleError struct{ Reason string }

func (e *IncompatibleError) Error() string { return e.Reason }

// ParseManifest 解析并校验 dir 下的 plugin.json。路径类字段必须落在 dir 之内（解析符号链接后检查）。
// appVersion 用来检查 min_app_version，"dev"（开发版）或空表示不检查。
func ParseManifest(dir, appVersion string) (*Manifest, error) {
	f, err := os.Open(filepath.Join(dir, FileName))
	if err != nil {
		return nil, fmt.Errorf("找不到 %s", FileName)
	}
	defer f.Close()
	buf := make([]byte, maxManifestBytes+1)
	n, _ := f.Read(buf)
	if n > maxManifestBytes {
		return nil, fmt.Errorf("%s 太大（超过 %d KB）", FileName, maxManifestBytes>>10)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(buf[:n]))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s 不是合法的 JSON：%v", FileName, err)
	}
	if err := m.validate(dir); err != nil {
		return nil, err
	}
	if err := m.compat(appVersion); err != nil {
		return &m, err
	}
	return &m, nil
}

func (m *Manifest) compat(appVersion string) error {
	if m.Protocol != 0 && m.Protocol != event.ProtocolVersion {
		return &IncompatibleError{fmt.Sprintf("需要升级应用：插件要求事件协议 v%d，当前是 v%d", m.Protocol, event.ProtocolVersion)}
	}
	if m.MinAppVersion != "" && appVersion != "" && appVersion != "dev" {
		if newer, err := update.Newer(m.MinAppVersion, appVersion); err == nil && newer {
			return &IncompatibleError{fmt.Sprintf("需要升级应用：插件要求 %s 或更高版本，当前是 %s", m.MinAppVersion, appVersion)}
		}
	}
	return nil
}

func (m *Manifest) validate(dir string) error {
	if !idRe.MatchString(m.ID) {
		return fmt.Errorf("id %q 不合法：只能用小写字母、数字和 -，以字母或数字开头，最长 24 个字符", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" || len([]rune(m.Name)) > 60 {
		return errors.New("name 不能为空，最长 60 个字符")
	}
	if _, err := update.Newer(m.Version, "0.0.0"); err != nil || len(m.Version) > 40 {
		return fmt.Errorf("version %q 不是合法的版本号（例如 1.2.3）", m.Version)
	}
	if len([]rune(m.Description)) > 500 {
		return errors.New("description 太长（最多 500 个字符）")
	}
	if m.MinAppVersion != "" {
		if _, err := update.Newer(m.MinAppVersion, "0.0.0"); err != nil {
			return fmt.Errorf("min_app_version %q 不是合法的版本号", m.MinAppVersion)
		}
	}
	if len(m.Hooks) > 0 && string(bytes.TrimSpace(m.Hooks)) != "null" {
		return errors.New("这个版本的应用还不支持钩子（hooks），请去掉清单里的 hooks")
	}
	if len(m.Subagents) > 0 && string(bytes.TrimSpace(m.Subagents)) != "null" {
		return errors.New("这个版本的应用还不支持子 agent 定义（subagents），请去掉清单里的 subagents")
	}

	// 配置项先校验：后面命令里的 ${config.xxx} 要对照它。
	keys := map[string]bool{}
	if len(m.Config) > maxConfig {
		return fmt.Errorf("config 最多 %d 项", maxConfig)
	}
	for _, c := range m.Config {
		if err := c.validate(); err != nil {
			return err
		}
		if keys[c.Key] {
			return fmt.Errorf("config 里有重复的 key %q", c.Key)
		}
		keys[c.Key] = true
	}

	if len(m.Skills) > maxSkills {
		return fmt.Errorf("skills 最多 %d 个", maxSkills)
	}
	for _, s := range m.Skills {
		p, err := insideDir(dir, s.Path)
		if err != nil {
			return fmt.Errorf("skills 路径 %q：%v", s.Path, err)
		}
		if fi, err := os.Stat(filepath.Join(p, "SKILL.md")); err != nil || fi.IsDir() {
			return fmt.Errorf("skills 路径 %q 下没有 SKILL.md", s.Path)
		}
	}

	if len(m.Commands) > maxCommands {
		return fmt.Errorf("commands 最多 %d 个", maxCommands)
	}
	seen := map[string]bool{}
	for _, c := range m.Commands {
		if !cmdNameRe.MatchString(c.Name) {
			return fmt.Errorf("命令名 %q 不合法：只能用小写字母、数字、. _ -，最长 40 个字符", c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("命令名 %q 重复", c.Name)
		}
		seen[c.Name] = true
		p, err := insideDir(dir, c.Path)
		if err != nil {
			return fmt.Errorf("命令 %q 的路径 %q：%v", c.Name, c.Path, err)
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() || fi.Size() > maxCommandBytes {
			return fmt.Errorf("命令 %q 的模板文件 %q 不存在或超过 %d KB", c.Name, c.Path, maxCommandBytes>>10)
		}
	}

	if len(m.MCP) > maxMCP {
		return fmt.Errorf("mcp 最多 %d 个", maxMCP)
	}
	servers := map[string]bool{}
	for _, s := range m.MCP {
		if err := s.validate(m.ID, dir, keys); err != nil {
			return err
		}
		name := m.ServerName(s)
		if servers[name] {
			return fmt.Errorf("mcp 服务名 %q 重复", name)
		}
		servers[name] = true
	}

	if m.Status != nil {
		if strings.TrimSpace(m.Status.Command) == "" {
			return errors.New("status.command 不能为空")
		}
		if err := checkVars(m.Status.Command, keys); err != nil {
			return fmt.Errorf("status.command：%v", err)
		}
	}
	if len(m.Actions) > maxActions {
		return fmt.Errorf("actions 最多 %d 个", maxActions)
	}
	ids := map[string]bool{}
	for _, a := range m.Actions {
		if !nameRe.MatchString(a.ID) || strings.TrimSpace(a.Label) == "" || strings.TrimSpace(a.Command) == "" {
			return fmt.Errorf("action %q 不合法：id 用小写字母数字和 -，label 和 command 不能为空", a.ID)
		}
		if ids[a.ID] {
			return fmt.Errorf("action id %q 重复", a.ID)
		}
		ids[a.ID] = true
		if err := checkVars(a.Command, keys); err != nil {
			return fmt.Errorf("action %q 的 command：%v", a.ID, err)
		}
	}
	for _, c := range m.Requires.Commands {
		if !reqNameRe.MatchString(c) {
			return fmt.Errorf("requires.commands 里的 %q 不合法", c)
		}
	}
	for _, h := range m.Network {
		if !hostRe.MatchString(h) {
			return fmt.Errorf("network 里的 %q 不是合法的域名", h)
		}
	}
	return nil
}

func (c ConfigField) validate() error {
	if !keyRe.MatchString(c.Key) {
		return fmt.Errorf("config key %q 不合法：小写字母开头，只能用小写字母、数字和 _，最长 32 个字符", c.Key)
	}
	if strings.TrimSpace(c.Label) == "" {
		return fmt.Errorf("config %q 缺少 label", c.Key)
	}
	switch c.Type {
	case TypeString, TypePath:
	case TypeSecret:
		if c.Default != "" {
			return fmt.Errorf("config %q 是 secret，不能有 default（密钥不能写进插件包）", c.Key)
		}
	case TypeBool:
		if c.Default != "" && c.Default != "true" && c.Default != "false" {
			return fmt.Errorf("config %q 的 default 必须是 true 或 false", c.Key)
		}
	case TypeSelect:
		if len(c.Options) == 0 || len(c.Options) > 50 {
			return fmt.Errorf("config %q 是 select，需要 1–50 个 options", c.Key)
		}
		if c.Default != "" && !contains(c.Options, c.Default) {
			return fmt.Errorf("config %q 的 default 不在 options 里", c.Key)
		}
	default:
		return fmt.Errorf("config %q 的类型 %q 不支持（可用：string、secret、bool、select、path）", c.Key, c.Type)
	}
	return nil
}

func (s MCPSpec) validate(pluginID, dir string, keys map[string]bool) error {
	if s.Name != "" && !nameRe.MatchString(s.Name) {
		return fmt.Errorf("mcp 服务名 %q 不合法：小写字母、数字和 -，最长 24 个字符", s.Name)
	}
	switch s.Risk {
	case "", permission.RiskRead, permission.RiskWrite, permission.RiskExec:
	default:
		return fmt.Errorf("mcp 服务的 risk %q 不合法（read / write / exec）", s.Risk)
	}
	switch s.Transport {
	case "", mcp.TransportStdio:
		if strings.TrimSpace(s.Command) == "" {
			return errors.New("mcp 服务缺少 command")
		}
	case mcp.TransportHTTP:
		if !strings.HasPrefix(s.URL, "https://") && !strings.HasPrefix(s.URL, "http://") {
			return fmt.Errorf("mcp 服务的 url %q 必须以 http:// 或 https:// 开头", s.URL)
		}
	default:
		return fmt.Errorf("mcp 服务的 transport %q 不支持（stdio / http）", s.Transport)
	}
	for k := range s.Env {
		if !envNameRe.MatchString(k) {
			return fmt.Errorf("mcp 的环境变量名 %q 不合法", k)
		}
	}
	if s.Cwd != "" {
		// cwd 里可能带变量；变量展开之后的路径在启动时再检查，这里只检查不带变量的相对路径。
		if !strings.Contains(s.Cwd, "${") {
			if _, err := insideDir(dir, s.Cwd); err != nil {
				return fmt.Errorf("mcp 的 cwd %q：%v", s.Cwd, err)
			}
		}
	}
	for _, v := range s.strings() {
		if err := checkVars(v, keys); err != nil {
			return fmt.Errorf("mcp 服务 %q：%v", s.Name, err)
		}
	}
	return nil
}

// strings 返回 MCP 声明里所有可能带变量的字符串。
func (s MCPSpec) strings() []string {
	out := []string{s.Command, s.Cwd, s.URL}
	out = append(out, s.Args...)
	for _, v := range s.Env {
		out = append(out, v)
	}
	for _, v := range s.Headers {
		out = append(out, v)
	}
	return out
}

// ServerName 是这个 MCP 声明注册到 mcp.Manager 时用的服务名（工具名是 mcp__<服务名>__<工具>）。
func (m *Manifest) ServerName(s MCPSpec) string {
	if s.Name == "" || s.Name == m.ID {
		return m.ID
	}
	return m.ID + "-" + s.Name
}

// checkVars 检查字符串里的 ${...} 都是已知变量：PLUGIN_DIR、DATA_DIR、config.<已声明的 key>。
// 未知变量是校验错误，不会被原样留下（否则一个拼错的变量会悄悄变成字面的 "${...}" 传给子进程）。
func checkVars(s string, keys map[string]bool) error {
	for _, m := range varRe.FindAllStringSubmatch(s, -1) {
		name := m[1]
		switch {
		case name == "PLUGIN_DIR" || name == "DATA_DIR":
		case strings.HasPrefix(name, "config.") && keys[strings.TrimPrefix(name, "config.")]:
		default:
			return fmt.Errorf("未知的变量 ${%s}", name)
		}
	}
	return nil
}

// insideDir 把 rel 解析成 dir 内的路径，拒绝绝对路径、.. 和（解析符号链接后）指到 dir 外的路径。
// 路径还不存在时只检查字面，存在时再解析符号链接。
func insideDir(dir, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("路径为空")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || (len(rel) > 1 && rel[1] == ':') {
		return "", errors.New("不能是绝对路径")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("不能指到插件目录之外")
	}
	p := filepath.Join(dir, clean)
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return "", err
	}
	if r, err := filepath.Rel(realDir, real); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("不能指到插件目录之外（符号链接）")
	}
	return real, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
