package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// ListDir 列出工作区内的目录。默认只列一层；recursive=true 时按树形递归（受 max_depth 限制），
// 递归时套用与 search_content 相同的忽略规则，否则一个 node_modules 就能吃掉整个上下文。
type ListDir struct {
	maxEntries int
	maxBytes   int
}

type listDirInput struct {
	Path          string `json:"path"`
	Recursive     bool   `json:"recursive"`
	MaxDepth      int    `json:"max_depth"`
	IncludeHidden bool   `json:"include_hidden"`
}

const defaultListEntries = 1000

func (t *ListDir) Spec() tool.Spec {
	return tool.Spec{
		Name:        "list_dir",
		Description: "列出工作区内的目录，目录以 / 结尾，目录在前、文件在后，按名称排序。recursive=true 时递归列出（默认最多 2 层），并忽略 .gitignore 中的文件和 node_modules、.git 等目录。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "目录路径，相对于工作区根目录，默认为 ."},
    "recursive": {"type": "boolean", "description": "是否递归，默认 false"},
    "max_depth": {"type": "integer", "description": "递归的最大层数，默认 2"},
    "include_hidden": {"type": "boolean", "description": "是否包含以 . 开头的文件和目录，默认 false"}
  }
}`),
		Risk: permission.RiskRead,
	}
}

func (t *ListDir) Summarize(input json.RawMessage) string {
	var in listDirInput
	_ = json.Unmarshal(input, &in)
	if in.Path == "" {
		in.Path = "."
	}
	if in.Recursive {
		return fmt.Sprintf("递归列出目录 %s", in.Path)
	}
	return fmt.Sprintf("列出目录 %s", in.Path)
}

func (t *ListDir) Execute(ctx context.Context, call tool.Call) tool.Result {
	var in listDirInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	path, err := resolvePath(call.Workspace, in.Path)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	if fi, err := os.Stat(path); err != nil {
		return tool.Errorf("未找到目录 %s，建议先列出上一级目录或用 find_files 查找", in.Path)
	} else if !fi.IsDir() {
		return tool.Errorf("%s 是文件不是目录，请用 read_file 读取", in.Path)
	}
	depth := 1
	if in.Recursive {
		depth = in.MaxDepth
		if depth <= 0 {
			depth = 2
		}
	}
	limit := t.maxEntries
	if limit <= 0 {
		limit = defaultListEntries
	}
	l := lister{ws: call.Workspace, ig: newIgnorer(call.Workspace), hidden: in.IncludeHidden, filter: in.Recursive, limit: limit}
	l.list(ctx, path, "", depth)
	if len(l.lines) == 0 {
		return tool.Result{Output: "(empty directory)"}
	}
	out := strings.Join(l.lines, "\n")
	if l.total > limit {
		out += fmt.Sprintf("\n...[已截断，共 %d 项，只显示前 %d 项。请指定更具体的 path 或减小 max_depth]", l.total, limit)
	}
	return tool.Result{Output: truncate(out, t.maxBytes)}
}

type lister struct {
	ws     string
	ig     *ignorer
	hidden bool
	filter bool // 是否套用忽略规则：只列一层时用户/模型显式要看这个目录，全部列出
	limit  int
	lines  []string
	total  int
}

// list 把 dir 的子项追加到 lines，子目录缩进两格继续递归。
func (l *lister) list(ctx context.Context, dir, indent string, depth int) {
	if depth <= 0 || ctx.Err() != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var dirs, files []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if !l.hidden && strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		if l.filter && l.ig.ignored(relPath(l.ws, full), e.IsDir()) {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	for _, d := range dirs {
		l.add(indent + d.Name() + "/")
		l.list(ctx, filepath.Join(dir, d.Name()), indent+"  ", depth-1)
	}
	for _, f := range files {
		l.add(indent + f.Name())
	}
}

func (l *lister) add(line string) {
	l.total++
	if len(l.lines) < l.limit {
		l.lines = append(l.lines, line)
	}
}
