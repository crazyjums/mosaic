package builtin

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/tool"
)

// Options 控制内置工具的行为。
type Options struct {
	// ShellTimeout 是 shell 工具单条命令的超时时间。
	ShellTimeout time.Duration
	// MaxOutputBytes 是返回给模型的最大输出字节数，超出部分截断。
	// 截断是为了保护上下文窗口：一次 `cat` 大文件就可能吃掉几万 token。
	MaxOutputBytes int
}

func (o Options) withDefaults() Options {
	if o.ShellTimeout <= 0 {
		o.ShellTimeout = 60 * time.Second
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = 32 * 1024
	}
	return o
}

// Register 把所有内置工具注册到 reg。
func Register(reg *tool.Registry, opts Options) {
	opts = opts.withDefaults()
	reg.MustRegister(&ReadFile{maxBytes: opts.MaxOutputBytes})
	reg.MustRegister(&WriteFile{})
	reg.MustRegister(&ListDir{})
	reg.MustRegister(&Shell{timeout: opts.ShellTimeout, maxBytes: opts.MaxOutputBytes})
}

// resolvePath 把模型给出的路径解析为工作区内的绝对路径。
//
// 规则：相对路径相对于工作区；绝对路径也必须落在工作区之内；`..` 逃逸一律拒绝。
// 没有处理符号链接逃逸（需要 EvalSymlinks，且文件可能还不存在），这是已知限制，见包注释。
func resolvePath(workspace, p string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("workspace is not set")
	}
	if p == "" {
		p = "."
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(workspace, p)
	}
	rel, err := filepath.Rel(workspace, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace", p)
	}
	return abs, nil
}

// truncate 截断过长的输出，并明确告诉模型"被截断了"，避免它误以为看到了全部内容。
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n...[truncated %d bytes]", len(s)-max)
}

// decode 解析工具参数。模型偶尔会给出不合法的 JSON，此时返回的错误会作为工具失败结果回传给模型，
// 让它自己修正，而不是让整个 agent 循环崩溃。
func decode(input json.RawMessage, v any) error {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	if err := json.Unmarshal(input, v); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}
