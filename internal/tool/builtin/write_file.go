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

// WriteFile 写入工作区内的文件（覆盖或追加），必要时创建父目录。
// 风险等级为 write：默认策略下每次执行前都需要用户确认。
type WriteFile struct{}

type writeFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode"` // overwrite（默认）| append
}

func (t *WriteFile) Spec() tool.Spec {
	return tool.Spec{
		Name:        "write_file",
		Description: "把内容写入工作区内的文件，自动创建父目录。mode=overwrite（默认）覆盖整个文件，mode=append 追加到末尾。只改文件的一部分时请用 edit_file。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "文件路径，相对于工作区根目录"},
    "content": {"type": "string", "description": "要写入的内容（overwrite 时是完整的文件内容）"},
    "mode": {"type": "string", "enum": ["overwrite", "append"], "description": "写入方式，默认 overwrite"}
  },
  "required": ["path", "content"]
}`),
		Risk: permission.RiskWrite,
	}
}

func (t *WriteFile) Summarize(input json.RawMessage) string {
	var in writeFileInput
	_ = json.Unmarshal(input, &in)
	if in.Mode == "append" {
		return fmt.Sprintf("追加到 %s（%d 字节）", in.Path, len(in.Content))
	}
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
	switch in.Mode {
	case "", "overwrite":
		_, statErr := os.Stat(path)
		if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
			return tool.Errorf("write failed: %v", err)
		}
		verb := "创建"
		if statErr == nil {
			verb = "覆盖"
		}
		return tool.Result{Output: fmt.Sprintf("已%s %s（%d 字节，%d 行）", verb, in.Path, len(in.Content), lineCount(in.Content))}
	case "append":
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return tool.Errorf("open failed: %v", err)
		}
		_, werr := f.WriteString(in.Content)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return tool.Errorf("write failed: %v", werr)
		}
		return tool.Result{Output: fmt.Sprintf("已追加 %d 字节到 %s", len(in.Content), in.Path)}
	default:
		return tool.Errorf("mode 只能是 overwrite 或 append，收到 %q", in.Mode)
	}
}
