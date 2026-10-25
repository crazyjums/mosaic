package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// ReadFile 读取工作区内的文本文件，支持按行分段（offset / limit）。
//
// 为什么默认限制 2000 行 / 200KB：一次读完大文件会挤掉上下文里更重要的内容；
// 超过时返回开头部分并提示用 offset/limit 继续读，同时告诉模型总行数，方便它规划。
type ReadFile struct{ maxBytes int }

type readFileInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

const (
	defaultReadLines = 2000
	maxReadBytes     = 200 * 1024
)

func (t *ReadFile) Spec() tool.Spec {
	return tool.Spec{
		Name:        "read_file",
		Description: "读取工作区内一个文本文件。默认最多读 2000 行；大文件请用 offset（起始行号，从 1 开始）和 limit（行数）分段读。读取部分内容时会在末尾注明总行数。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "文件路径，相对于工作区根目录"},
    "offset": {"type": "integer", "description": "从第几行开始读（从 1 开始），默认 1"},
    "limit": {"type": "integer", "description": "最多读多少行，默认 2000"}
  },
  "required": ["path"]
}`),
		Risk: permission.RiskRead,
	}
}

func (t *ReadFile) Summarize(input json.RawMessage) string {
	var in readFileInput
	_ = json.Unmarshal(input, &in)
	if in.Offset > 0 || in.Limit > 0 {
		return fmt.Sprintf("读取 %s（从第 %d 行起）", in.Path, max(in.Offset, 1))
	}
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
	fi, err := os.Stat(path)
	if err != nil {
		return tool.Errorf("未找到文件 %s，建议先用 find_files 或 search_content 确认路径", in.Path)
	}
	if fi.IsDir() {
		return tool.Errorf("%s 是目录，请用 list_dir 查看", in.Path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.Errorf("read failed: %v", err)
	}
	// 二进制文件直接交给模型既浪费 token 又没有意义。
	if !utf8.Valid(data) {
		return tool.Errorf("%s looks like a binary file (%d bytes), refusing to read", in.Path, len(data))
	}
	content := string(data)
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	offset, limit := max(in.Offset, 1), in.Limit
	if limit <= 0 {
		limit = defaultReadLines
	}
	if total > 0 && offset > total {
		return tool.Errorf("offset %d 超出文件范围（%s 共 %d 行）", offset, in.Path, total)
	}
	end := min(offset-1+limit, total)
	var b strings.Builder
	partial := offset > 1 || end < total
	budget := maxReadBytes
	if t.maxBytes > 0 {
		budget = min(budget, t.maxBytes) // 与统一的输出上限对齐，超出时给出分段提示而不是生硬截断
	}
	for i := offset - 1; i < end; i++ {
		// 第一行就超长（压缩过的 js）时仍然输出它，交给下面的 truncate 截断。
		if b.Len() > 0 && b.Len()+len(lines[i]) > budget {
			end, partial = i, true
			break
		}
		b.WriteString(lines[i])
	}
	out := b.String()
	if partial {
		out = strings.TrimSuffix(out, "\n") + fmt.Sprintf("\n...[显示第 %d-%d 行，共 %d 行。请用 offset/limit 分段读取其余部分]", offset, end, total)
	}
	return tool.Result{Output: truncate(out, t.maxBytes)}
}
