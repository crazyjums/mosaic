package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/crazyjums/myllmapp/internal/core"
)

const (
	maxFileBytes   = 64 << 10
	maxDescription = 1000
	maxAgents      = 50
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// toolNames 是 Claude Code 工具名 → Mosaic 内置工具名。没有对应物的不在表里（解析时记 Warning）。
var toolNames = map[string][]string{
	"read":      {"read_file"},
	"write":     {"write_file"},
	"edit":      {"edit_file"},
	"multiedit": {"edit_file"},
	"glob":      {"find_files"},
	"grep":      {"search_content"},
	"ls":        {"list_dir"},
	"bash":      {"shell"},
	"todowrite": {"todo_write", "todo_read"},
	"todoread":  {"todo_read"},
}

// mosaicTools 是 Mosaic 自己的内置工具名：直接写这些名字也认。
var mosaicTools = map[string]bool{
	"read_file": true, "write_file": true, "edit_file": true, "delete_file": true, "move_file": true, "mkdir": true,
	"list_dir": true, "find_files": true, "search_content": true, "shell": true,
	"git_status": true, "git_diff": true, "git_log": true, "todo_read": true, "todo_write": true, "read_skill": true,
}

// MapTools 把工具名列表翻译成 Mosaic 的名字。Claude Code 里写成 Bash(git *) 这样带说明的，只取工具名。
// 返回对得上的名字（去重、保持顺序）和对不上的原始名字。
func MapTools(names []string) (mapped, unknown []string) {
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			mapped = append(mapped, n)
		}
	}
	for _, raw := range names {
		n := strings.TrimSpace(raw)
		if i := strings.IndexByte(n, '('); i > 0 {
			n = strings.TrimSpace(n[:i])
		}
		switch {
		case n == "":
		case strings.HasPrefix(n, "mcp__"):
			add(n) // MCP 工具同名
		case mosaicTools[n]:
			add(n)
		case toolNames[strings.ToLower(n)] != nil:
			for _, m := range toolNames[strings.ToLower(n)] {
				add(m)
			}
		default:
			unknown = append(unknown, n)
		}
	}
	return mapped, unknown
}

// Parse 解析一个 agent 文件。nameHint 是 frontmatter 里没写 name 时用的名字（文件名）。
func Parse(data []byte, nameHint string) (core.SubagentDefinition, error) {
	if len(data) > maxFileBytes {
		return core.SubagentDefinition{}, fmt.Errorf("文件太大（超过 %d KB）", maxFileBytes>>10)
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\xef\xbb\xbf"), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return core.SubagentDefinition{}, errors.New("开头没有 --- 包起来的 frontmatter（需要 name 和 description）")
	}
	head, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return core.SubagentDefinition{}, errors.New("frontmatter 没有用 --- 结束")
	}
	body = strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(body, "-"), "\n"))
	fm := parseFrontmatter(head)

	d := core.SubagentDefinition{Name: strings.TrimSpace(fm.str("name")), Description: strings.TrimSpace(fm.str("description")), System: body}
	if d.Name == "" {
		d.Name = nameHint
	}
	if !nameRe.MatchString(d.Name) {
		return d, fmt.Errorf("name %q 不合法（字母、数字、. _ -，最长 64）", d.Name)
	}
	if d.Description == "" {
		return d, errors.New("缺少 description：主 agent 靠它判断什么时候该派这个子 agent")
	}
	if r := []rune(d.Description); len(r) > maxDescription {
		d.Description = string(r[:maxDescription])
	}
	if d.System == "" {
		return d, errors.New("正文是空的：正文就是子 agent 的系统提示词")
	}

	var unknown []string
	if tools := fm.list("tools"); len(tools) > 0 {
		d.Tools, unknown = MapTools(tools)
		if len(d.Tools) == 0 {
			d.Tools = []string{"__none__"} // 写了白名单却一个都对不上：保留“有白名单”的语义，运行时会拒绝而不是放开全部工具
		}
	} else if _, declared := fm["tools"]; declared {
		// 写了 tools 却没读出任何一项（写法看不懂，或者是空的）：宁可不运行，也不当成“没限制”给它全部工具。
		d.Tools = []string{"__none__"}
		d.Warnings = append(d.Warnings, "tools 写了但没读出任何工具（写法不支持或为空），为安全起见这个子 agent 不会运行")
	}
	if dis := fm.list("disallowedTools"); len(dis) > 0 {
		var u2 []string
		d.DisallowedTools, u2 = MapTools(dis)
		unknown = append(unknown, u2...)
	}
	if len(unknown) > 0 {
		d.Warnings = append(d.Warnings, "这些工具 Mosaic 没有对应的，已忽略："+strings.Join(unknown, "、"))
	}
	if m := strings.TrimSpace(fm.str("model")); m != "" && m != "inherit" {
		d.Model = m
	}
	if mt := strings.TrimSpace(fm.str("maxTurns")); mt != "" {
		if n, err := strconv.Atoi(mt); err == nil && n > 0 {
			d.MaxSteps = n
		}
	}
	for _, k := range []string{"permissionMode", "hooks", "mcpServers"} {
		if fm.has(k) {
			d.Warnings = append(d.Warnings, k+" 不生效：子 agent 和主对话用同一套权限，不能通过定义文件放宽权限或带进可执行组件")
		}
	}
	return d, nil
}

// LoadDir 扫描目录里的 *.md（含子目录）。prefix 非空时名字变成 “prefix:名字”（插件的 agent）。
// 单个文件有问题只跳过它，问题记在 problems 里。
func LoadDir(dir, prefix string) (defs []core.SubagentDefinition, problems []string) {
	var files []string
	_ = filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() && strings.HasPrefix(e.Name(), ".") && p != dir {
			return filepath.SkipDir
		}
		if !e.IsDir() && strings.EqualFold(filepath.Ext(p), ".md") && e.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	seen := map[string]bool{}
	for _, f := range files {
		if len(defs) >= maxAgents {
			problems = append(problems, fmt.Sprintf("子 agent 超过 %d 个，其余的没有加载", maxAgents))
			break
		}
		rel, _ := filepath.Rel(dir, f)
		data, err := os.ReadFile(f)
		if err != nil {
			problems = append(problems, rel+"："+err.Error())
			continue
		}
		hint := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		d, err := Parse(data, hint)
		if err != nil {
			problems = append(problems, rel+"："+err.Error())
			continue
		}
		if prefix != "" {
			// 子目录也是名字的一部分（和 Claude Code 一致）：agents/review/security.md → 插件:review:security
			parts := strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/")
			name := prefix
			for _, p := range parts {
				if p != "." && p != "" {
					name += ":" + p
				}
			}
			d.Name = name + ":" + d.Name
		}
		if seen[d.Name] {
			problems = append(problems, rel+"：名字 "+d.Name+" 重复，跳过")
			continue
		}
		seen[d.Name] = true
		defs = append(defs, d)
	}
	return defs, problems
}

// frontmatter 是极简的 YAML 子集：key: value、key: [a, b]、以及 key: 下面一行行的 “- 项”。
type frontmatter map[string][]string

func parseFrontmatter(head string) frontmatter {
	fm := frontmatter{}
	var listKey string
	for _, line := range strings.Split(head, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// YAML 允许列表项和键对齐（“- Read” 顶格写），所以不要求缩进；只要前面是一个值为空的键就算它的项。
		if item, ok := strings.CutPrefix(trimmed, "- "); ok && listKey != "" {
			fm[listKey] = append(fm[listKey], unquote(item))
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		listKey = ""
		switch {
		case v == "":
			listKey = k
			fm[k] = []string{}
		case strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]"):
			for _, x := range strings.Split(strings.Trim(v, "[]"), ",") {
				if x = unquote(strings.TrimSpace(x)); x != "" {
					fm[k] = append(fm[k], x)
				}
			}
		default:
			fm[k] = []string{unquote(v)}
		}
	}
	return fm
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

func (f frontmatter) has(k string) bool { _, ok := f[k]; return ok }

func (f frontmatter) str(k string) string {
	if v := f[k]; len(v) > 0 {
		return strings.Join(v, ", ")
	}
	return ""
}

// list 取列表字段：YAML 列表，或逗号分隔的字符串。
func (f frontmatter) list(k string) []string {
	var out []string
	for _, v := range f[k] {
		for _, x := range strings.Split(v, ",") {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
	}
	return out
}
