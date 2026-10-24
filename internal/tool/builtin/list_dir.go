package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// ListDir 列出工作区内某个目录的直接子项（不递归）。
// 不递归是有意的：递归列出大仓库会产生海量输出，让模型按需逐层查看更省上下文。
type ListDir struct{}

type listDirInput struct {
	Path string `json:"path"`
}

func (t *ListDir) Spec() tool.Spec {
	return tool.Spec{
		Name:        "list_dir",
		Description: "列出工作区内某个目录的直接子项，目录以 / 结尾。不传 path 表示工作区根目录。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "目录路径，相对于工作区根目录，默认为 ."}
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
	return fmt.Sprintf("列出目录 %s", in.Path)
}

func (t *ListDir) Execute(_ context.Context, call tool.Call) tool.Result {
	var in listDirInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	path, err := resolvePath(call.Workspace, in.Path)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return tool.Errorf("list failed: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return tool.Result{Output: "(empty directory)"}
	}
	return tool.Result{Output: strings.Join(names, "\n")}
}
