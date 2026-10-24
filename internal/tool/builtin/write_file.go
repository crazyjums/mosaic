package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// WriteFile 写入（覆盖）工作区内的文件，必要时创建父目录。
// 风险等级为 write：默认策略下每次执行前都需要用户确认。
type WriteFile struct{}

type writeFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *WriteFile) Spec() tool.Spec {
	return tool.Spec{
		Name:        "write_file",
		Description: "把内容写入工作区内的文件（覆盖已有内容，自动创建父目录）。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "文件路径，相对于工作区根目录"},
    "content": {"type": "string", "description": "完整的文件内容"}
  },
  "required": ["path", "content"]
}`),
		Risk: permission.RiskWrite,
	}
}

func (t *WriteFile) Summarize(input json.RawMessage) string {
	var in writeFileInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("写入 %s（%d 字节）", in.Path, len(in.Content))
}

func (t *WriteFile) Execute(_ context.Context, call tool.Call) tool.Result {
	var in writeFileInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	if in.Path == "" {
		return tool.Errorf("path is required")
	}
	path, err := resolvePath(call.Workspace, in.Path)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return tool.Errorf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return tool.Errorf("write failed: %v", err)
	}
	return tool.Result{Output: fmt.Sprintf("wrote %d bytes to %s", len(in.Content), in.Path)}
}
