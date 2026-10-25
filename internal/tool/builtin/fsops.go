package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// 目录和文件管理：mkdir、move_file、delete_file。都是 write 风险，默认需要用户确认。
//
// delete_file 不真正删除，而是移进工作区的 .myllmapp/trash/：模型删错了文件，用户还能找回来。
// 回收站在内置忽略名单里（.myllmapp），不会出现在搜索和列表结果中。

// TrashDir 是回收站相对工作区的路径。
const TrashDir = ".myllmapp/trash"

// Mkdir 创建目录（含父目录）。
type Mkdir struct{}

type pathInput struct {
	Path string `json:"path"`
}

func (t *Mkdir) Spec() tool.Spec {
	return tool.Spec{
		Name:        "mkdir",
		Description: "在工作区内创建目录（自动创建父目录，已存在不报错）。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {"path": {"type": "string", "description": "目录路径，相对于工作区根目录"}},
  "required": ["path"]
}`),
		Risk: permission.RiskWrite,
	}
}

func (t *Mkdir) Summarize(input json.RawMessage) string {
	var in pathInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("创建目录 %s", in.Path)
}

func (t *Mkdir) Execute(_ context.Context, call tool.Call) tool.Result {
	var in pathInput
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
	if err := os.MkdirAll(path, 0o755); err != nil {
		return tool.Errorf("mkdir failed: %v", err)
	}
	return tool.Result{Output: fmt.Sprintf("已创建目录 %s", in.Path)}
}

// MoveFile 移动或重命名文件/目录。目标已存在时拒绝，避免悄悄覆盖。
type MoveFile struct{}

type moveInput struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func (t *MoveFile) Spec() tool.Spec {
	return tool.Spec{
		Name:        "move_file",
		Description: "移动或重命名工作区内的文件或目录（自动创建目标的父目录）。目标已存在时会报错，不会覆盖。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "source": {"type": "string", "description": "原路径，相对于工作区根目录"},
    "destination": {"type": "string", "description": "新路径，相对于工作区根目录"}
  },
  "required": ["source", "destination"]
}`),
		Risk: permission.RiskWrite,
	}
}

func (t *MoveFile) Summarize(input json.RawMessage) string {
	var in moveInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("移动 %s → %s", in.Source, in.Destination)
}

func (t *MoveFile) Execute(_ context.Context, call tool.Call) tool.Result {
	var in moveInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	if in.Source == "" || in.Destination == "" {
		return tool.Errorf("source 和 destination 不能为空")
	}
	src, err := resolvePath(call.Workspace, in.Source)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	dst, err := resolvePath(call.Workspace, in.Destination)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	if src == filepath.Clean(call.Workspace) {
		return tool.Errorf("不能移动工作区根目录")
	}
	if _, err := os.Lstat(src); err != nil {
		return tool.Errorf("未找到 %s，建议先用 list_dir 或 find_files 确认路径", in.Source)
	}
	if _, err := os.Lstat(dst); err == nil {
		return tool.Errorf("目标 %s 已存在。如需替换，请先 delete_file 删除它", in.Destination)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return tool.Errorf("mkdir failed: %v", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return tool.Errorf("move failed: %v", err)
	}
	return tool.Result{Output: fmt.Sprintf("已移动 %s → %s", in.Source, in.Destination)}
}

// DeleteFile 把文件或目录移进回收站。
type DeleteFile struct{}

func (t *DeleteFile) Spec() tool.Spec {
	return tool.Spec{
		Name:        "delete_file",
		Description: "删除工作区内的文件或目录。不会真正删除，而是移到 " + TrashDir + "/ 下，用户可以找回。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {"path": {"type": "string", "description": "要删除的路径，相对于工作区根目录"}},
  "required": ["path"]
}`),
		Risk: permission.RiskWrite,
	}
}

func (t *DeleteFile) Summarize(input json.RawMessage) string {
	var in pathInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("删除 %s（移到回收站）", in.Path)
}

func (t *DeleteFile) Execute(_ context.Context, call tool.Call) tool.Result {
	var in pathInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	if in.Path == "" {
		return tool.Errorf("path is required")
	}
	src, err := resolvePath(call.Workspace, in.Path)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	trash := filepath.Join(call.Workspace, filepath.FromSlash(TrashDir))
	if src == filepath.Clean(call.Workspace) || within(filepath.Join(call.Workspace, ".myllmapp"), src) {
		return tool.Errorf("不能删除工作区根目录或 .myllmapp 目录")
	}
	if _, err := os.Lstat(src); err != nil {
		return tool.Errorf("未找到 %s，建议先用 list_dir 确认路径", in.Path)
	}
	// 回收站里按时间戳分目录，保留原来的相对路径：同名文件删两次也不会互相覆盖。
	dst := filepath.Join(trash, time.Now().Format("20060102-150405.000"), relPath(call.Workspace, src))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return tool.Errorf("mkdir failed: %v", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return tool.Errorf("delete failed: %v", err)
	}
	return tool.Result{Output: fmt.Sprintf("已删除 %s（移到了 %s）", in.Path, relPath(call.Workspace, dst))}
}
