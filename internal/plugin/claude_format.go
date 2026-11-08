package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/permission"
)

// Claude Code 格式的插件（.claude-plugin/plugin.json + 约定的目录）。
//
// 为什么要读懂它：现成的插件绝大多数是这个布局，只认根目录 plugin.json 的话，用户拿到的插件十有八九装不上。
// 字段和目录约定依据 Claude Code 官方的插件参考（plugins-reference）：
//   - 清单可选，放在 .claude-plugin/plugin.json，只有 name 必填；没有清单时按默认目录找组件
//   - 默认位置：skills/<名>/SKILL.md、commands/*.md（扁平）、agents/、hooks/hooks.json、.mcp.json、.lsp.json ……
//   - 清单里的 skills 是追加，commands / agents 是替换默认目录；mcpServers 与 .mcp.json 合并
//   - 变量：${CLAUDE_PLUGIN_ROOT}、${CLAUDE_PLUGIN_DATA}、${CLAUDE_PROJECT_DIR}、${user_config.键}
//
// 这里只做“把它翻译成 Mosaic 自己的 Manifest”：纯读文件，不执行任何东西。Mosaic 不支持的组件
// （LSP、输出风格……）不会生效，也**不会被悄悄忽略**：列进 Manifest.Ignored，安装确认和详情里如实告诉用户。
// hooks 会执行任意命令：导入后每一条都要用户单独批准才会运行（见 hooks.go），绝不静默授予。

const claudeDir = ".claude-plugin"

// claudeManifest 是 .claude-plugin/plugin.json 里我们用到的字段；其余忽略（官方也是忽略未知字段）。
type claudeManifest struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"displayName"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Skills      json.RawMessage `json:"skills"`
	Commands    json.RawMessage `json:"commands"`
	Agents      json.RawMessage `json:"agents"`
	Hooks       json.RawMessage `json:"hooks"`
	MCPServers  json.RawMessage `json:"mcpServers"`
	LSPServers  json.RawMessage `json:"lspServers"`
	UserConfig  map[string]struct {
		Type        string   `json:"type"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Required    bool     `json:"required"`
		Default     any      `json:"default"`
		Options     []string `json:"options"`
		Sensitive   bool     `json:"sensitive"`
	} `json:"userConfig"`
}

// isClaudeLayout 判断目录是不是 Claude Code 布局：有 .claude-plugin/plugin.json，或没有清单但有约定的组件目录。
func isClaudeLayout(dir string) bool {
	// 原生 plugin.json 和 Codex 的 .codex-plugin 优先：同时带着 .claude-plugin 的插件按它们的格式读。
	if fileExists(filepath.Join(dir, FileName)) || fileExists(filepath.Join(dir, ".codex-plugin", FileName)) {
		return false
	}
	if fileExists(filepath.Join(dir, claudeDir, FileName)) {
		return true
	}
	for _, rel := range []string{"skills", "commands", "agents", "hooks"} {
		if isDir(filepath.Join(dir, rel)) {
			return true
		}
	}
	return fileExists(filepath.Join(dir, ".mcp.json")) || fileExists(filepath.Join(dir, "SKILL.md"))
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// sanitizeID 把任意名字变成合法的插件 id（小写字母数字和 -，最长 24）。同一个输入永远得到同一个结果：
// 已安装的插件每次加载都要重新推出同一个 id（目录名就是 id）。
func sanitizeID(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 24 {
		out = strings.Trim(out[:24], "-")
	}
	return out
}

func sanitizeCmdName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-.")
	}
	return out
}

// importClaudePlugin 把 Claude Code 布局的目录翻译成 Manifest。hint 是没有清单名字时用的名字（目录名、仓库名等）。
func importClaudePlugin(dir, hint string) (*Manifest, error) {
	var cm claudeManifest
	if raw, err := os.ReadFile(filepath.Join(dir, claudeDir, FileName)); err == nil {
		if len(raw) > maxManifestBytes {
			return nil, fmt.Errorf("%s/%s 太大（超过 %d KB）", claudeDir, FileName, maxManifestBytes>>10)
		}
		if err := json.Unmarshal(raw, &cm); err != nil {
			return nil, fmt.Errorf("%s/%s 不是合法的 JSON：%v", claudeDir, FileName, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	name := strings.TrimSpace(cm.Name)
	if name == "" {
		name = strings.TrimSpace(hint)
	}
	id := sanitizeID(name)
	if id == "" {
		return nil, errors.New("这个插件没有名字：清单里没有 name，也无法从来源推出（请在 .claude-plugin/plugin.json 里写上 name）")
	}
	m := &Manifest{ID: id, Name: firstNonEmpty(cm.DisplayName, cm.Name, name), Version: strings.TrimSpace(cm.Version), Description: cm.Description, Format: "claude"}
	if m.Version == "" {
		m.Version = "0.0.0"
	}
	if r := []rune(m.Name); len(r) > 60 {
		m.Name = string(r[:60])
	}
	if r := []rune(m.Description); len(r) > 500 {
		m.Description = string(r[:500])
	}

	imp := &claudeImport{dir: dir, m: m, keys: map[string]string{}}
	imp.userConfig(cm)
	imp.skills(cm)
	imp.commands(cm)
	imp.mcp(cm)
	imp.agents(cm)
	imp.hooks(cm)
	imp.ignoredComponents(cm)

	if len(m.Skills) == 0 && len(m.Commands) == 0 && len(m.MCP) == 0 && len(m.Agents) == 0 && len(m.HookDecls) == 0 {
		msg := "这个插件里没有 Mosaic 能用的内容（技能、命令、MCP 服务、子 agent、钩子）"
		if len(m.Ignored) > 0 {
			msg += "；它只有这些不受支持的组件：" + strings.Join(m.Ignored, "、")
		}
		return nil, errors.New(msg)
	}
	sort.Strings(m.Requires.Commands)
	return m, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

type claudeImport struct {
	dir  string
	m    *Manifest
	keys map[string]string // Claude 的 userConfig 键 → Mosaic 的 config 键
}

func (c *claudeImport) ignore(format string, a ...any) {
	c.m.Ignored = append(c.m.Ignored, fmt.Sprintf(format, a...))
}

// ---- 路径字段：字符串或字符串数组，都必须以 ./ 开头并落在插件目录内 ----

func rawStrings(raw json.RawMessage) ([]string, bool) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, true
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many, true
	}
	return nil, false
}

// ---- 配置项 ----

var userConfigKeyRe = regexp.MustCompile(`[^a-z0-9_]`)

func (c *claudeImport) userConfig(cm claudeManifest) {
	keys := make([]string, 0, len(cm.UserConfig))
	for k := range cm.UserConfig {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	usedKeys := map[string]bool{}
	for _, k := range keys {
		o := cm.UserConfig[k]
		key := userConfigKeyRe.ReplaceAllString(strings.ToLower(k), "_")
		if key == "" || key[0] < 'a' || key[0] > 'z' {
			key = "k_" + key
		}
		if len(key) > 32 {
			key = key[:32]
		}
		// 归一化会让不同的键撞在一起（Region 和 region、截断、标点）：给后来的加数字后缀，保持唯一。
		// keys 是按排序后的原始键遍历的，所以同一个插件每次得到同样的映射。
		base := key
		for n := 2; usedKeys[key]; n++ {
			suffix := fmt.Sprintf("_%d", n)
			if len(base)+len(suffix) > 32 {
				key = base[:32-len(suffix)] + suffix
			} else {
				key = base + suffix
			}
		}
		usedKeys[key] = true
		if len(c.m.Config) >= maxConfig {
			c.ignore("配置项 %s（超过 %d 项上限）", k, maxConfig)
			continue
		}
		f := ConfigField{Key: key, Label: firstNonEmpty(o.Title, k), Help: o.Description, Required: o.Required}
		switch {
		case o.Sensitive:
			f.Type = TypeSecret
		case o.Type == "boolean":
			f.Type = TypeBool
		case o.Type == "directory" || o.Type == "file":
			f.Type = TypePath
		case len(o.Options) > 0:
			f.Type, f.Options = TypeSelect, o.Options
		default:
			f.Type = TypeString
		}
		switch d := o.Default.(type) {
		case string:
			f.Default = d
		case bool:
			f.Default = fmt.Sprint(d)
		case float64:
			f.Default = fmt.Sprint(d)
		}
		if f.Type == TypeSecret {
			f.Default = "" // 密钥不能有默认值
		}
		if f.Type == TypeBool && f.Default == "" {
			f.Default = "false"
		}
		if f.validate() != nil {
			c.ignore("配置项 %s（格式不被支持）", k)
			continue
		}
		c.keys[k] = key
		c.m.Config = append(c.m.Config, f)
	}
}

// ---- 技能 ----

func (c *claudeImport) skills(cm claudeManifest) {
	seen := map[string]bool{}
	add := func(rel string) {
		rel = filepath.ToSlash(filepath.Clean(rel))
		if seen[rel] {
			return
		}
		if len(c.m.Skills) >= maxSkills {
			c.ignore("技能 %s（超过 %d 个上限）", rel, maxSkills)
			return
		}
		seen[rel] = true
		c.m.Skills = append(c.m.Skills, SkillRef{Path: rel})
	}
	// scan 在目录下找技能：目录本身有 SKILL.md 就是一个技能，否则它的每个含 SKILL.md 的子目录是一个技能。
	scan := func(rel string) {
		p, err := insideDir(c.dir, rel)
		if err != nil || !isDir(p) {
			return
		}
		if fileExists(filepath.Join(p, "SKILL.md")) {
			add(rel)
			return
		}
		entries, _ := os.ReadDir(p)
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && fileExists(filepath.Join(p, e.Name(), "SKILL.md")) {
				add(filepath.ToSlash(filepath.Join(rel, e.Name())))
			}
		}
	}
	paths, ok := rawStrings(cm.Skills)
	if !ok {
		c.ignore("清单里的 skills 写法不被支持")
	}
	// 清单里的 skills 是追加：默认的 skills/ 照常扫描。
	scan("skills")
	for _, p := range paths {
		scan(strings.TrimPrefix(p, "./"))
	}
	// 根目录有 SKILL.md、没有 skills/ 也没有 skills 字段：整个插件就是一个技能。
	if len(c.m.Skills) == 0 && len(paths) == 0 && fileExists(filepath.Join(c.dir, "SKILL.md")) {
		add(".")
	}
}

// ---- 命令 ----

func (c *claudeImport) commands(cm claudeManifest) {
	add := func(name, rel, desc string) {
		name = sanitizeCmdName(name)
		if !cmdNameRe.MatchString(name) {
			c.ignore("命令 %s（名字不合法）", rel)
			return
		}
		for _, x := range c.m.Commands {
			if x.Name == name {
				c.ignore("命令 %s（与另一个命令重名）", rel)
				return
			}
		}
		if len(c.m.Commands) >= maxCommands {
			c.ignore("命令 %s（超过 %d 个上限）", rel, maxCommands)
			return
		}
		p, err := insideDir(c.dir, rel)
		if err != nil {
			c.ignore("命令 %s（%v）", rel, err)
			return
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() || fi.Size() > maxCommandBytes {
			c.ignore("命令 %s（文件不存在或超过 %d KB）", rel, maxCommandBytes>>10)
			return
		}
		if t, err := loadCommandTemplate(p); err != nil {
			c.ignore("命令 %s（%v）", rel, err)
			return
		} else {
			if desc == "" {
				desc = t.description
			}
			if strings.Contains(t.body, "!`") {
				c.ignore("命令 /%s 里的 !`…` 内联命令不会被执行（其余内容照常可用）", name)
			}
		}
		c.m.Commands = append(c.m.Commands, CommandRef{Name: name, Path: filepath.ToSlash(rel), Description: desc, claude: true})
	}
	addDir := func(rel string) {
		p, err := insideDir(c.dir, rel)
		if err != nil || !isDir(p) {
			return
		}
		entries, _ := os.ReadDir(p)
		for _, e := range entries {
			switch {
			case e.IsDir():
				c.ignore("命令子目录 %s（Mosaic 的命令是扁平的，没有命名空间）", filepath.ToSlash(filepath.Join(rel, e.Name())))
			case strings.EqualFold(filepath.Ext(e.Name()), ".md"):
				add(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())), filepath.ToSlash(filepath.Join(rel, e.Name())), "")
			}
		}
	}

	raw := []byte(strings.TrimSpace(string(cm.Commands)))
	if len(raw) == 0 || string(raw) == "null" {
		addDir("commands") // 没写 commands 字段：扫默认目录
		return
	}
	// 写了 commands 字段：替换默认目录。它可以是路径、路径数组，或“命令名 → {source|content}”的对象。
	if paths, ok := rawStrings(raw); ok {
		for _, p := range paths {
			rel := strings.TrimPrefix(p, "./")
			if abs, err := insideDir(c.dir, rel); err == nil && isDir(abs) {
				addDir(rel)
			} else {
				add(strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)), rel, "")
			}
		}
		return
	}
	var obj map[string]struct {
		Source      string `json:"source"`
		Content     string `json:"content"`
		Description string `json:"description"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		c.ignore("清单里的 commands 写法不被支持")
		return
	}
	names := make([]string, 0, len(obj))
	for n := range obj {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		o := obj[n]
		if o.Source == "" {
			c.ignore("命令 %s（内联 content 的命令暂不支持，请改成 .md 文件）", n)
			continue
		}
		add(n, strings.TrimPrefix(o.Source, "./"), o.Description)
	}
}

// ---- MCP 服务 ----

// claudeMCPServer 是 Claude Code 的 MCP 服务声明（.mcp.json 的每一项）。
type claudeMCPServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// parseMCPFile 读一份 MCP 配置：{"mcpServers": {...}}，或者直接是 {"名字": {...}} 的映射。
func parseMCPFile(raw []byte) (map[string]claudeMCPServer, error) {
	var wrapped struct {
		MCPServers map[string]claudeMCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.MCPServers != nil {
		return wrapped.MCPServers, nil
	}
	var flat map[string]json.RawMessage
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, err
	}
	out := map[string]claudeMCPServer{}
	for k, v := range flat {
		var s claudeMCPServer
		if json.Unmarshal(v, &s) == nil && (s.Command != "" || s.URL != "") {
			out[k] = s
		}
	}
	return out, nil
}

// 变量映射：Claude Code 的写法 → Mosaic 的写法。
var claudeVarRe = regexp.MustCompile(`\$\{([^}]*)\}`)

// translateVars 把字符串里的 Claude Code 变量换成 Mosaic 的；遇到 Mosaic 没有对应物的变量返回 false。
func (c *claudeImport) translateVars(s string) (string, bool) {
	ok := true
	out := claudeVarRe.ReplaceAllStringFunc(s, func(m string) string {
		name := claudeVarRe.FindStringSubmatch(m)[1]
		switch {
		case name == "CLAUDE_PLUGIN_ROOT":
			return "${PLUGIN_DIR}"
		case name == "CLAUDE_PLUGIN_DATA":
			return "${DATA_DIR}"
		case strings.HasPrefix(name, "user_config."):
			if key, found := c.keys[strings.TrimPrefix(name, "user_config.")]; found {
				return "${config." + key + "}"
			}
		}
		ok = false
		return m
	})
	return out, ok
}

func (c *claudeImport) mcp(cm claudeManifest) {
	servers := map[string]claudeMCPServer{}
	var order []string
	merge := func(src map[string]claudeMCPServer) {
		names := make([]string, 0, len(src))
		for n := range src {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if _, dup := servers[n]; !dup {
				order = append(order, n)
			}
			servers[n] = src[n] // 后声明的替换先声明的（官方规则）
		}
	}
	readFile := func(rel string) {
		p, err := insideDir(c.dir, rel)
		if err != nil {
			c.ignore("MCP 配置 %s（%v）", rel, err)
			return
		}
		if ext := strings.ToLower(filepath.Ext(p)); ext == ".mcpb" || ext == ".dxt" {
			c.ignore("MCP 打包文件 %s（.mcpb / .dxt 暂不支持）", rel)
			return
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			if !os.IsNotExist(err) {
				c.ignore("MCP 配置 %s（读取失败）", rel)
			}
			return
		}
		if len(raw) > maxManifestBytes {
			c.ignore("MCP 配置 %s（太大）", rel)
			return
		}
		src, err := parseMCPFile(raw)
		if err != nil {
			c.ignore("MCP 配置 %s（不是合法的 JSON）", rel)
			return
		}
		merge(src)
	}
	readFile(".mcp.json") // 先读默认文件，再按清单里声明的顺序合并

	raw := []byte(strings.TrimSpace(string(cm.MCPServers)))
	if len(raw) > 0 && string(raw) != "null" {
		var items []json.RawMessage
		if raw[0] == '[' {
			_ = json.Unmarshal(raw, &items)
		} else {
			items = []json.RawMessage{raw}
		}
		for _, it := range items {
			var path string
			if json.Unmarshal(it, &path) == nil {
				if strings.HasPrefix(path, "https://") {
					c.ignore("MCP 打包文件 %s（暂不支持）", path)
				} else {
					readFile(strings.TrimPrefix(path, "./"))
				}
				continue
			}
			if src, err := parseMCPFile(it); err == nil {
				merge(src)
			} else {
				c.ignore("清单里的 mcpServers 写法不被支持")
			}
		}
	}

	for _, name := range order {
		s := servers[name]
		id := sanitizeID(name)
		if id == "" {
			c.ignore("MCP 服务 %s（名字不合法）", name)
			continue
		}
		if len(c.m.MCP) >= maxMCP {
			c.ignore("MCP 服务 %s（超过 %d 个上限）", name, maxMCP)
			continue
		}
		spec := MCPSpec{Name: id, Risk: permission.RiskExec} // 风险档位一律保守：用户在确认页勾选信任后才降级
		switch strings.ToLower(s.Type) {
		case "", "stdio":
			spec.Transport, spec.Command = mcp.TransportStdio, s.Command
			if s.Command == "" {
				if s.URL == "" {
					c.ignore("MCP 服务 %s（没有 command）", name)
					continue
				}
				spec.Transport = mcp.TransportHTTP // 没写 type 但有 url：按远程服务处理
			}
		case "http", "streamable-http":
			spec.Transport = mcp.TransportHTTP
		default:
			c.ignore("MCP 服务 %s（传输类型 %s 暂不支持，只支持 stdio 和 http）", name, s.Type)
			continue
		}
		okAll := true
		tr := func(v string) string {
			out, ok := c.translateVars(v)
			okAll = okAll && ok
			return out
		}
		spec.Command, spec.URL = tr(s.Command), tr(s.URL)
		for _, a := range s.Args {
			spec.Args = append(spec.Args, tr(a))
		}
		if len(s.Env) > 0 {
			spec.Env = map[string]string{}
			for k, v := range s.Env {
				spec.Env[k] = tr(v)
			}
		}
		if len(s.Headers) > 0 {
			spec.Headers = map[string]string{}
			for k, v := range s.Headers {
				spec.Headers[k] = tr(v)
			}
		}
		if !okAll {
			c.ignore("MCP 服务 %s（用到了 Mosaic 没有的变量，例如 ${CLAUDE_PROJECT_DIR}）", name)
			continue
		}
		c.m.MCP = append(c.m.MCP, spec)
		// 提前检查本机有没有这个可执行文件，而不是等握手超时才发现。
		if spec.Transport == mcp.TransportStdio && reqNameRe.MatchString(spec.Command) && !contains(c.m.Requires.Commands, spec.Command) {
			c.m.Requires.Commands = append(c.m.Requires.Commands, spec.Command)
		}
	}
}

// ---- 子 agent ----

// agents 收集子 agent 文件（相对路径）。默认扫描 agents/（递归）；清单里写了 agents 就替换默认目录
// （和 Claude Code 一致），它只能是 .md 文件。文件内容在启用时由装配层解析（internal/agents）。
func (c *claudeImport) agents(cm claudeManifest) {
	const maxAgentFiles = 50
	add := func(rel string) {
		rel = filepath.ToSlash(filepath.Clean(rel))
		p, err := insideDir(c.dir, rel)
		if err != nil {
			c.ignore("子 agent %s（%v）", rel, err)
			return
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() || !strings.EqualFold(filepath.Ext(p), ".md") {
			c.ignore("子 agent %s（不是 .md 文件或不存在）", rel)
			return
		}
		if len(c.m.Agents) >= maxAgentFiles {
			c.ignore("子 agent %s（超过 %d 个上限）", rel, maxAgentFiles)
			return
		}
		if !contains(c.m.Agents, rel) {
			c.m.Agents = append(c.m.Agents, rel)
		}
	}
	if paths, ok := rawStrings(cm.Agents); ok && len(paths) > 0 {
		for _, p := range paths {
			add(strings.TrimPrefix(p, "./"))
		}
		return
	} else if !ok {
		c.ignore("清单里的 agents 写法不被支持（只能是 .md 文件路径或它们的列表）")
		return
	}
	_ = filepath.WalkDir(filepath.Join(c.dir, "agents"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".md") && d.Type().IsRegular() {
			if rel, err := filepath.Rel(c.dir, p); err == nil {
				add(rel)
			}
		}
		return nil
	})
	sort.Strings(c.m.Agents)
}

// ---- 不支持的组件：列出来，不悄悄忽略 ----

func (c *claudeImport) ignoredComponents(cm claudeManifest) {
	if fileExists(filepath.Join(c.dir, ".lsp.json")) || len(strings.TrimSpace(string(cm.LSPServers))) > 4 {
		c.ignore("语言服务器（LSP）")
	}
	for _, d := range []struct{ rel, label string }{
		{"output-styles", "输出风格（output-styles）"}, {"monitors", "后台监视器（monitors）"}, {"workflows", "工作流（workflows）"},
		{"themes", "主题（themes）"}, {"bin", "bin/ 里的可执行文件（不会加入 PATH）"},
	} {
		if isDir(filepath.Join(c.dir, d.rel)) {
			c.ignore("%s", d.label)
		}
	}
	if fileExists(filepath.Join(c.dir, "settings.json")) {
		c.ignore("插件自带的默认设置（settings.json）")
	}
}

// hintFromSource 从安装来源推出一个名字（别家格式的插件没写名字时用）：目录名、zip 去掉扩展名、Git 地址的仓库名。
func hintFromSource(source string) string {
	s := strings.TrimSpace(source)
	if i := strings.Index(s, "#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(filepath.ToSlash(s), "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, ".zip")
	return strings.TrimPrefix(s, "bundled:")
}
