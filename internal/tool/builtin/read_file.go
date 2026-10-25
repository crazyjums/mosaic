package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// ReadFile 读取工作区内的文本文件。
type ReadFile struct{ maxBytes int }

type readFileInput struct {
	Path string `json:"path"`
}

func (t *ReadFile) Spec() tool.Spec {
	return tool.Spec{
		Name:        "read_file",
		Description: "读取工作区内一个文本文件的内容。路径相对于工作区根目录。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "文件路径，相对于工作区根目录"}
  },
  "required": ["path"]
}`),
		Risk: permission.RiskRead,
	}
}

func (t *ReadFile) Summarize(input json.RawMessage) string {
	var in readFileInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("读取 %s", in.Path)
}

func (t *ReadFile) Execute(_ context.Context, call tool.Call) tool.Result {
	var in readFileInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	path, err := resolvePath(call.Workspace, in.Path)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.Errorf("read failed: %v", err)
	}
	// 二进制文件直接交给模型既浪费 token 又没有意义。
	if !utf8.Valid(data) {
		return tool.Errorf("%s looks like a binary file (%d bytes), refusing to read", in.Path, len(data))
	}
	return tool.Result{Output: truncate(string(data), t.maxBytes)}
}
