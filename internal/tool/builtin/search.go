package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// SearchContent 在工作区里按正则搜索文件内容，相当于内置的 grep。
//
// 用 Go 实现而不是调用系统 grep/rg：Windows 上没有 grep，而且自己实现才能统一忽略规则和输出格式。
// 多个文件并行扫描（每个 CPU 一个 worker），结果再按路径和行号排序，保证输出稳定。
type SearchContent struct{ maxBytes int }

type searchInput struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path"`
	Glob          string `json:"glob"`
	CaseSensitive bool   `json:"case_sensitive"`
	MaxResults    int    `json:"max_results"`
}

const (
	defaultSearchResults = 100
	maxSearchFileBytes   = 2 << 20 // 跳过超过 2MB 的文件：多半是生成物或数据
	maxSearchLineRunes   = 300     // 单行过长（压缩过的 js）只显示开头
)

func (t *SearchContent) Spec() tool.Spec {
	return tool.Spec{
		Name:        "search_content",
		Description: "在工作区文件中按正则表达式搜索内容（类似 grep），输出 `路径:行号: 内容`。默认忽略 .gitignore 中的文件以及 node_modules、.git、dist、build 等目录，跳过二进制文件。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "正则表达式（Go RE2 语法）"},
    "path": {"type": "string", "description": "搜索的目录或文件，相对于工作区根目录，默认 ."},
    "glob": {"type": "string", "description": "只搜索匹配的文件，例如 *.go 或 src/**/*.ts"},
    "case_sensitive": {"type": "boolean", "description": "区分大小写，默认 false"},
    "max_results": {"type": "integer", "description": "最多返回的命中行数，默认 100"}
  },
  "required": ["pattern"]
}`),
		Risk: permission.RiskRead,
	}
}

func (t *SearchContent) Summarize(input json.RawMessage) string {
	var in searchInput
	_ = json.Unmarshal(input, &in)
	if in.Glob != "" {
		return fmt.Sprintf("搜索 %q（%s）", in.Pattern, in.Glob)
	}
	return fmt.Sprintf("搜索 %q", in.Pattern)
}

type searchHit struct {
	path string
	line int
	text string
}

func (t *SearchContent) Execute(ctx context.Context, call tool.Call) tool.Result {
	var in searchInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	if in.Pattern == "" {
		return tool.Errorf("pattern is required")
	}
	expr := in.Pattern
	if !in.CaseSensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return tool.Errorf("正则表达式无效：%v。注意要转义特殊字符，例如 \\( \\. \\[", err)
	}
	limit := in.MaxResults
	if limit <= 0 {
		limit = defaultSearchResults
	}
	root, err := resolvePath(call.Workspace, in.Path)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	fi, err := os.Stat(root)
	if err != nil {
		return tool.Errorf("未找到 %s，建议先用 list_dir 确认路径", in.Path)
	}

	// 先收集候选文件，再并行扫描。
	var files []string
	if fi.IsDir() {
		ig := newIgnorer(call.Workspace)
		_ = walkFiles(call.Workspace, root, ig, func(rel string, d os.DirEntry) error {
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if d.Type().IsRegular() && (in.Glob == "" || matchFileGlob(in.Glob, rel)) {
				files = append(files, filepath.Join(call.Workspace, rel))
			}
			return nil
		})
	} else {
		files = []string{root}
	}

	var (
		mu    sync.Mutex
		hits  []searchHit
		total int
		wg    sync.WaitGroup
	)
	jobs := make(chan string)
	for range max(1, runtime.NumCPU()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				found := scanFile(f, re)
				if len(found) == 0 {
					continue
				}
				rel := relPath(call.Workspace, f)
				mu.Lock()
				total += len(found)
				for _, h := range found {
					h.path = rel
					hits = append(hits, h)
				}
				mu.Unlock()
			}
		}()
	}
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return tool.Errorf("cancelled")
	}

	if total == 0 {
		return tool.Result{Output: fmt.Sprintf("没有找到匹配 %q 的内容（搜索了 %d 个文件）。可以换个关键词、关闭大小写敏感，或用 find_files 按文件名查找", in.Pattern, len(files))}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].path != hits[j].path {
			return hits[i].path < hits[j].path
		}
		return hits[i].line < hits[j].line
	})
	var b strings.Builder
	for i, h := range hits {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "%s:%d: %s\n", h.path, h.line, h.text)
	}
	if total > limit {
		fmt.Fprintf(&b, "...[已截断，共 %d 处命中，只显示前 %d 处。请缩小 path / glob 或使用更精确的 pattern]\n", total, limit)
	}
	return tool.Result{Output: truncate(strings.TrimRight(b.String(), "\n"), t.maxBytes)}
}

// scanFile 返回文件中所有匹配的行。二进制文件（含 NUL）和过大的文件直接跳过。
func scanFile(path string, re *regexp.Regexp) []searchHit {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() > maxSearchFileBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return nil
	}
	var out []searchHit
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), maxSearchFileBytes)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if re.MatchString(line) {
			out = append(out, searchHit{line: n, text: clipLine(strings.TrimRight(line, "\r"))})
		}
	}
	return out
}

func clipLine(s string) string {
	r := []rune(s)
	if len(r) <= maxSearchLineRunes {
		return s
	}
	return string(r[:maxSearchLineRunes]) + "…"
}

// FindFiles 按 glob 模式查找文件（按文件名找，search_content 是按内容找）。
type FindFiles struct{ maxBytes int }

type findInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	MaxResults int    `json:"max_results"`
}

func (t *FindFiles) Spec() tool.Spec {
	return tool.Spec{
		Name:        "find_files",
		Description: "按 glob 模式查找文件，例如 *.go、**/*_test.go、src/**/index.ts。不含 / 的模式匹配文件名。忽略规则与 search_content 相同。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "glob 模式，支持 * ? [...] 和 **"},
    "path": {"type": "string", "description": "从哪个目录开始找，默认 ."},
    "max_results": {"type": "integer", "description": "最多返回的文件数，默认 200"}
  },
  "required": ["pattern"]
}`),
		Risk: permission.RiskRead,
	}
}

func (t *FindFiles) Summarize(input json.RawMessage) string {
	var in findInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("查找文件 %s", in.Pattern)
}

func (t *FindFiles) Execute(ctx context.Context, call tool.Call) tool.Result {
	var in findInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	if in.Pattern == "" {
		return tool.Errorf("pattern is required")
	}
	limit := in.MaxResults
	if limit <= 0 {
		limit = 200
	}
	root, err := resolvePath(call.Workspace, in.Path)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return tool.Errorf("目录 %s 不存在，建议先用 list_dir 确认路径", in.Path)
	}
	var found []string
	total := 0
	_ = walkFiles(call.Workspace, root, newIgnorer(call.Workspace), func(rel string, d os.DirEntry) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if !d.IsDir() && matchFileGlob(in.Pattern, rel) {
			total++
			if len(found) < limit {
				found = append(found, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if total == 0 {
		return tool.Result{Output: fmt.Sprintf("没有匹配 %s 的文件。不含 / 的模式只匹配文件名；要匹配路径请写成 **/dir/*.ext", in.Pattern)}
	}
	sort.Strings(found)
	out := strings.Join(found, "\n")
	if total > limit {
		out += fmt.Sprintf("\n...[已截断，共 %d 个文件，只显示前 %d 个。请使用更精确的模式]", total, limit)
	}
	return tool.Result{Output: truncate(out, t.maxBytes)}
}
