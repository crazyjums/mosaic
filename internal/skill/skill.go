package skill

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// 技能的来源。
const (
	SourceProject = "project" // 工作区里的
	SourceUser    = "user"    // 用户目录里的
)

const (
	// FileName 是技能说明文件。
	FileName = "SKILL.md"
	// maxSkills 限制一次最多发现多少个技能：目录里被人塞进成千上万个文件夹时，不至于拖慢每一轮。
	maxSkills = 200
	// maxHeaderBytes：读 SKILL.md 开头这么多字节来找 frontmatter。
	maxHeaderBytes = 16 * 1024
	// MaxBodyBytes 是一次返回给模型的正文上限。
	MaxBodyBytes = 100 * 1024
	// MaxResourceBytes 是单个资源文件的读取上限。
	MaxResourceBytes = 200 * 1024
	// maxListFiles 是 read_skill 里列出的资源文件数量上限。
	maxListFiles = 60
	maxNameLen   = 64
	maxDescLen   = 1024
)

// Info 是一个技能的概要（不含正文）。
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Source：project、user，或 "plugin:<id>"（插件带来的）。Origin 是它所在的根目录，例如 ".myllmapp/skills"，用来区分同名时哪个生效。
	Source string `json:"source"`
	Origin string `json:"origin"`
	// Dir 是技能文件夹的绝对路径。
	Dir string `json:"dir"`
}

// Folder 是调用方额外提供的一个技能文件夹（里面有 SKILL.md），例如插件带来的技能。
// 和 root 不同：root 是“装着很多技能文件夹的目录”，Folder 直接就是技能文件夹本身。
type Folder struct {
	// Dir 是技能文件夹的绝对路径。
	Dir string
	// Source 标明来源，例如 "plugin:feishu"；Origin 是界面上显示的出处，例如插件名。
	Source string
	Origin string
}

// root 是一个搜索位置。
type root struct {
	base, source, origin string
}

func roots(workspace, home string) []root {
	var out []root
	add := func(base, source, rel string) {
		if base != "" {
			out = append(out, root{base: filepath.Join(base, filepath.FromSlash(rel)), source: source, origin: rel})
		}
	}
	add(workspace, SourceProject, ".myllmapp/skills")
	add(workspace, SourceProject, ".claude/skills")
	add(home, SourceUser, ".myllmapp/skills")
	add(home, SourceUser, ".claude/skills")
	return out
}

// Discover 找出可用的技能，按名字排序。workspace 或 home 可以为空。
// 读不了的目录、格式不对的技能都直接跳过（技能是锦上添花，不能因为一个坏文件让整个应用报错）。
//
// extra 是额外的技能文件夹（插件提供的），排在所有目录之后：同名时项目和个人的技能优先，插件不会覆盖用户自己写的技能。
func Discover(workspace, home string, extra ...Folder) []Info {
	seen := map[string]bool{}
	var out []Info
	for _, r := range roots(workspace, home) {
		entries, err := os.ReadDir(r.base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if len(out) >= maxSkills {
				break
			}
			dir := filepath.Join(r.base, e.Name())
			// 符号链接指向的文件夹也算（很多人把技能仓库链接进来）。
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, err := readInfo(dir, e.Name())
			if err != nil || seen[info.Name] {
				continue
			}
			info.Source, info.Origin = r.source, r.origin
			seen[info.Name] = true
			out = append(out, info)
		}
	}
	for _, f := range extra {
		if len(out) >= maxSkills {
			break
		}
		if fi, err := os.Stat(f.Dir); err != nil || !fi.IsDir() {
			continue
		}
		info, err := readInfo(f.Dir, filepath.Base(f.Dir))
		if err != nil || seen[info.Name] {
			continue
		}
		info.Source, info.Origin = f.Source, f.Origin
		seen[info.Name] = true
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Find 按名字找技能（名字不区分大小写）。
func Find(workspace, home, name string, extra ...Folder) (Info, bool) {
	name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	for _, s := range Discover(workspace, home, extra...) {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return Info{}, false
}

func readInfo(dir, folder string) (Info, error) {
	f, err := os.Open(filepath.Join(dir, FileName))
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	buf := make([]byte, maxHeaderBytes)
	n, _ := f.Read(buf)
	meta, body := splitFrontmatter(string(buf[:n]))
	name := meta["name"]
	if name == "" {
		name = folder
	}
	if !validName(name) {
		return Info{}, fmt.Errorf("skill: invalid name %q", name)
	}
	desc := meta["description"]
	if desc == "" {
		desc = firstParagraph(body) // 没写 description 时取正文第一段，总比空着强
	}
	return Info{Name: name, Description: truncateRunes(desc, maxDescLen), Dir: dir}, nil
}

// validName：字母数字（含中文）、-、_、.，不能含路径分隔符和空白。名字会出现在 /名字 里，也会拿来比较，所以要严格一点。
func validName(s string) bool {
	if s == "" || len(s) > maxNameLen {
		return false
	}
	for _, r := range s {
		switch {
		case r == '-' || r == '_' || r == '.':
		case r > 127 && !isSpace(r):
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return s[0] != '.'
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '　' || r == ' ' }

// splitFrontmatter 解析开头的 "---" 块里的顶层 key: value。支持带引号的值，以及 > / | 开头的多行值。
// 不依赖 YAML 库：只需要 name 和 description 两个字段，写一个很小的解析器比引入依赖更合适。
func splitFrontmatter(text string) (map[string]string, string) {
	text = strings.TrimPrefix(text, "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	meta := map[string]string{}
	if !strings.HasPrefix(text, "---\n") {
		return meta, text
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, text
	}
	header := rest[:end]
	body := strings.TrimPrefix(rest[end+4:], "\n")

	lines := strings.Split(header, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if strings.HasPrefix(v, ">") || strings.HasPrefix(v, "|") {
			fold := strings.HasPrefix(v, ">")
			var parts []string
			for i+1 < len(lines) && (lines[i+1] == "" || lines[i+1][0] == ' ' || lines[i+1][0] == '\t') {
				i++
				parts = append(parts, strings.TrimSpace(lines[i]))
			}
			sep := "\n"
			if fold {
				sep = " "
			}
			v = strings.TrimSpace(strings.Join(parts, sep))
		} else {
			v = unquote(v)
		}
		meta[strings.ToLower(k)] = v
	}
	return meta, body
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

func firstParagraph(body string) string {
	var heading string
	for _, p := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "#") {
			if heading == "" {
				heading = strings.Join(strings.Fields(strings.TrimLeft(p, "# ")), " ")
			}
			continue // 标题不是简介，继续找第一段正文
		}
		return strings.Join(strings.Fields(p), " ")
	}
	return heading
}

func truncateRunes(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// Body 读取技能正文（去掉 frontmatter），超出 MaxBodyBytes 的部分截断并注明。
func Body(s Info) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, FileName))
	if err != nil {
		return "", err
	}
	_, body := splitFrontmatter(string(data))
	body = strings.TrimSpace(body)
	if len(body) > MaxBodyBytes {
		cut := MaxBodyBytes
		for cut > 0 && !utf8.RuneStart(body[cut]) {
			cut--
		}
		body = body[:cut] + "\n...[truncated]"
	}
	return body, nil
}

// Files 列出技能文件夹里的资源文件（相对路径，用 / 分隔），不含 SKILL.md 和隐藏文件，最多 maxListFiles 个。
func Files(s Info) []string {
	var out []string
	_ = filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != s.Dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(s.Dir, p)
		if rel == FileName {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= maxListFiles {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// ErrOutside 表示请求的资源文件不在技能文件夹内。
var ErrOutside = errors.New("skill: path is outside the skill folder")

// ReadResource 读取技能文件夹内的一个文本资源文件。rel 是相对技能文件夹的路径。
// 路径经过符号链接解析后必须仍在技能文件夹内；超过 MaxResourceBytes 的文件只返回开头；二进制文件报错。
func ReadResource(s Info, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", ErrOutside
	}
	base, err := filepath.EvalSymlinks(s.Dir)
	if err != nil {
		return "", err
	}
	p, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	if r, err := filepath.Rel(base, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", ErrOutside
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", fmt.Errorf("skill: %s is a directory", rel)
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data := make([]byte, MaxResourceBytes+1)
	n, _ := io.ReadFull(f, data) // 文件比缓冲区短时返回 ErrUnexpectedEOF，n 仍然是读到的字节数
	data = data[:n]
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("skill: %s is not a text file", rel) // 含 NUL 字节：按二进制处理
	}
	text := string(data)
	if len(data) > MaxResourceBytes {
		cut := MaxResourceBytes
		for cut > 0 && !utf8.RuneStart(data[cut]) {
			cut--
		}
		text = string(data[:cut]) + "\n...[truncated]"
	}
	return text, nil
}

// Prompt 生成追加到系统提示词后面的技能清单；没有任何技能时返回空串（不改变提示词，也不影响提示缓存）。
func Prompt(workspace, home string, extra ...Folder) string {
	list := Discover(workspace, home, extra...)
	if len(list) == 0 {
		return ""
	}
	const maxListed = 60
	var b strings.Builder
	b.WriteString("\n\nSkills (reusable instructions written by the user). Each entry is `name: when to use it`. When one applies, load its full text first with the read_skill tool, then follow it. If the user's message starts with /skill-name, they are asking you to use that skill.\n")
	for i, s := range list {
		if i >= maxListed {
			fmt.Fprintf(&b, "- (%d more skills; use read_skill with a name to load one)\n", len(list)-maxListed)
			break
		}
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, truncateRunes(s.Description, 240))
	}
	return strings.TrimRight(b.String(), "\n")
}
