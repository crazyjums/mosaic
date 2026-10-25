package builtin

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// 遍历类工具（search_content / find_files / list_dir 递归）共用的忽略规则。
//
// 为什么集中在一处：三个工具各写一份，迟早会出现"搜得到但列不出来"之类的不一致。
// 顺序：先看工作区根目录的 .gitignore（用户最清楚哪些是生成物），再套内置黑名单。
// 内置黑名单总是生效：即使仓库没写 .gitignore，node_modules 之类的目录也会让输出爆炸。

// builtinIgnoredDirs 是总是跳过的目录名。
var builtinIgnoredDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true, ".myllmapp": true,
	".idea": true, ".next": true, ".cache": true,
}

// ignorer 判断工作区内的某个相对路径（用 / 分隔）是否应被跳过。
type ignorer struct {
	rules []ignoreRule
}

type ignoreRule struct {
	pattern  string
	negate   bool // !pattern：重新包含
	dirOnly  bool // pattern/：只匹配目录
	anchored bool // 含 /：相对于根目录匹配完整路径；否则匹配任意一级的名字
}

// newIgnorer 读取工作区根目录的 .gitignore。读不到就只用内置黑名单。
//
// 只支持 .gitignore 的常用子集（*、?、[...]、**、前导 /、结尾 /、!），不读子目录里的 .gitignore：
// 完整实现很复杂，而这里的目的只是"别把生成物塞给模型"，偶尔多搜到一个文件没有害处。
func newIgnorer(workspace string) *ignorer {
	ig := &ignorer{}
	f, err := os.Open(filepath.Join(workspace, ".gitignore"))
	if err != nil {
		return ig
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{}
		if strings.HasPrefix(line, "!") {
			r.negate, line = true, line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimRight(line, "/")
		}
		if strings.Contains(line, "/") {
			r.anchored, line = true, strings.TrimPrefix(line, "/")
		}
		if line == "" {
			continue
		}
		r.pattern = line
		ig.rules = append(ig.rules, r)
	}
	return ig
}

// ignored 判断相对路径 rel 是否被忽略。isDir 表示它是目录。
// 调用方在遍历时遇到被忽略的目录应整个跳过（filepath.SkipDir），所以这里只需判断最后一级。
func (ig *ignorer) ignored(rel string, isDir bool) bool {
	rel = filepath.ToSlash(rel)
	name := path.Base(rel)
	if isDir && builtinIgnoredDirs[name] {
		return true
	}
	// 后出现的规则优先，和 git 一致。
	out := false
	for _, r := range ig.rules {
		if r.dirOnly && !isDir {
			continue
		}
		var hit bool
		if r.anchored {
			hit = globMatch(r.pattern, rel)
		} else {
			hit = globMatch(r.pattern, name)
		}
		if hit {
			out = !r.negate
		}
	}
	return out
}

// globMatch 匹配用 / 分隔的路径，支持 **（任意多级目录）以及 path.Match 的 * ? [...]。
func globMatch(pattern, name string) bool {
	return matchParts(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchParts(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchParts(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// matchFileGlob 是 find_files / search_content 的 glob 过滤：
// 不含 / 的模式（如 *.go）匹配文件名，含 / 的模式（如 src/**/*.ts）匹配相对工作区的完整路径。
func matchFileGlob(pattern, rel string) bool {
	rel = filepath.ToSlash(rel)
	if !strings.Contains(pattern, "/") {
		return globMatch(pattern, path.Base(rel))
	}
	return globMatch(strings.TrimPrefix(pattern, "./"), rel)
}

// walkFiles 遍历 dir（工作区内的绝对路径）下所有未被忽略的条目，fn 收到相对工作区的路径。
// fn 返回 filepath.SkipAll 可以提前结束。不跟随符号链接（WalkDir 本身就不跟随），
// 避免经链接走出工作区或陷入循环。
func walkFiles(workspace, dir string, ig *ignorer, fn func(rel string, d os.DirEntry) error) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 读不了的目录跳过，不让一个权限问题毁掉整次搜索
		}
		rel, _ := filepath.Rel(workspace, p)
		if p != dir && ig.ignored(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return fn(rel, d)
	})
}

// relPath 把工作区内的绝对路径转成给模型看的相对路径（统一用 /）。
func relPath(workspace, p string) string {
	rel, err := filepath.Rel(workspace, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}
