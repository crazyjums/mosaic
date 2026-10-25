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

// EditFile 对文件做精确的字符串替换。
//
// 为什么需要它：只有 write_file 时，改一行也得把整个文件重新输出一遍，既费 token 又容易
// "顺手"改坏别处。精确替换要求 old_string 唯一匹配，模型拿不准位置时会被迫多带上下文。
type EditFile struct{}

type editFileInput struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *EditFile) Spec() tool.Spec {
	return tool.Spec{
		Name:        "edit_file",
		Description: "精确替换文件中的一段文本。old_string 必须与文件内容逐字一致（包括缩进）且只出现一次；出现多次时请带上更多上下文，或设置 replace_all=true 全部替换。修改前请先 read_file 确认内容。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "文件路径，相对于工作区根目录"},
    "old_string": {"type": "string", "description": "要被替换的原文，必须逐字一致"},
    "new_string": {"type": "string", "description": "替换后的文本"},
    "replace_all": {"type": "boolean", "description": "替换所有出现的位置，默认 false"}
  },
  "required": ["path", "old_string", "new_string"]
}`),
		Risk: permission.RiskWrite,
	}
}

func (t *EditFile) Summarize(input json.RawMessage) string {
	var in editFileInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("编辑 %s", in.Path)
}

func (t *EditFile) Execute(_ context.Context, call tool.Call) tool.Result {
	var in editFileInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	if in.Path == "" || in.OldString == "" {
		return tool.Errorf("path 和 old_string 不能为空（新建文件请用 write_file）")
	}
	if in.OldString == in.NewString {
		return tool.Errorf("old_string 与 new_string 相同，没有需要修改的内容")
	}
	path, err := resolvePath(call.Workspace, in.Path)
	if err != nil {
		return tool.Errorf("%v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return tool.Errorf("未找到文件 %s，建议先用 find_files 或 list_dir 确认路径", in.Path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.Errorf("read failed: %v", err)
	}
	if !utf8.Valid(data) {
		return tool.Errorf("%s 看起来是二进制文件或非 UTF-8 编码，不能用 edit_file 修改", in.Path)
	}
	content := string(data)

	// 保持换行风格：文件用 CRLF 时，模型给出的 LF 文本也转成 CRLF 再匹配和写回。
	// 模型几乎总是用 \n 输出，否则 Windows 上的文件会一直"未找到"。
	oldS, newS := in.OldString, in.NewString
	if strings.Contains(content, "\r\n") {
		oldS, newS = toCRLF(oldS), toCRLF(newS)
	}

	n := strings.Count(content, oldS)
	switch {
	case n == 0:
		return tool.Errorf("在 %s 中未找到 old_string。请先 read_file 查看当前内容（注意缩进和空白必须逐字一致），或用 search_content 定位", in.Path)
	case n > 1 && !in.ReplaceAll:
		return tool.Errorf("old_string 在 %s 中出现了 %d 次（第 %s 行）。请带上更多上下文使它唯一，或设置 replace_all=true", in.Path, n, matchLines(content, oldS, 10))
	}
	firstLine := strings.Count(content[:strings.Index(content, oldS)], "\n") + 1
	var updated string
	if in.ReplaceAll {
		updated = strings.ReplaceAll(content, oldS, newS)
	} else {
		updated = strings.Replace(content, oldS, newS, 1)
	}
	// 写回时保留原文件的权限位。
	if err := os.WriteFile(path, []byte(updated), fi.Mode().Perm()); err != nil {
		return tool.Errorf("write failed: %v", err)
	}
	removed, added := lineCount(oldS)*n, lineCount(newS)*n
	where := fmt.Sprintf("第 %d 行", firstLine)
	if n > 1 {
		where = fmt.Sprintf("%d 处，首处在第 %d 行", n, firstLine)
	}
	return tool.Result{Output: fmt.Sprintf("已修改 %s（%s）：+%d -%d 行", in.Path, where, added, removed)}
}

func toCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// lineCount 是一段文本占的行数（空串为 0）。
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// matchLines 列出 sub 在 s 中各次出现的起始行号（最多 max 个），帮助模型挑出想改的那一处。
func matchLines(s, sub string, max int) string {
	var lines []string
	off := 0
	for len(lines) < max {
		i := strings.Index(s[off:], sub)
		if i < 0 {
			break
		}
		lines = append(lines, fmt.Sprint(strings.Count(s[:off+i], "\n")+1))
		off += i + len(sub)
	}
	return strings.Join(lines, "、")
}
