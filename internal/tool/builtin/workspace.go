package builtin

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/crazyjums/myllmapp/internal/skill"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/crazyjums/myllmapp/internal/tool"
)

// Options 控制内置工具的行为。
type Options struct {
	// ShellTimeout 是 shell 工具单条命令的超时时间。
	ShellTimeout time.Duration
	// SkillFolders 返回插件提供的额外技能文件夹（read_skill 每次调用时取，插件启停立即生效）；可以为 nil。
	SkillFolders func() []skill.Folder
	// MaxOutputBytes 是返回给模型的最大输出字节数，超出部分截断。
	// 截断是为了保护上下文窗口：一次 `cat` 大文件就可能吃掉几万 token。
	MaxOutputBytes int
}

// DefaultMaxOutputBytes 是所有内置工具统一的输出上限（100KB），超出部分截断并注明。
const DefaultMaxOutputBytes = 100 * 1024

// MaxShellTimeout 是模型通过 timeout_seconds 能申请的最长超时。
const MaxShellTimeout = 600 * time.Second

// DefaultShellTimeout 是 shell 工具的默认超时。
const DefaultShellTimeout = 60 * time.Second

func (o Options) withDefaults() Options {
	if o.ShellTimeout <= 0 {
		o.ShellTimeout = DefaultShellTimeout
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = DefaultMaxOutputBytes
	}
	return o
}

// Register 把所有内置工具注册到 reg。
func Register(reg *tool.Registry, opts Options) {
	opts = opts.withDefaults()
	// 注册顺序就是发给模型的工具顺序：先读后写，最常用的在前。
	reg.MustRegister(&ReadFile{maxBytes: opts.MaxOutputBytes})
	reg.MustRegister(&ListDir{maxBytes: opts.MaxOutputBytes})
	reg.MustRegister(&FindFiles{maxBytes: opts.MaxOutputBytes})
	reg.MustRegister(&SearchContent{maxBytes: opts.MaxOutputBytes})
	reg.MustRegister(&EditFile{})
	reg.MustRegister(&WriteFile{})
	reg.MustRegister(&Mkdir{})
	reg.MustRegister(&MoveFile{})
	reg.MustRegister(&DeleteFile{})
	reg.MustRegister(NewShell(opts.ShellTimeout, opts.MaxOutputBytes))
	reg.MustRegister(&GitStatus{maxBytes: opts.MaxOutputBytes})
	reg.MustRegister(&GitDiff{maxBytes: opts.MaxOutputBytes})
	reg.MustRegister(&GitLog{maxBytes: opts.MaxOutputBytes})
	todos := newTodoStore()
	reg.MustRegister(&TodoWrite{store: todos})
	reg.MustRegister(&TodoRead{store: todos})
	// 技能：个人技能放在用户目录里；读不到用户目录时只搜索工作区里的技能。
	home, _ := os.UserHomeDir()
	reg.MustRegister(NewReadSkill(home, opts.SkillFolders))
}

// resolvePath 把模型给出的路径解析为工作区内的绝对路径。
//
// 规则：相对路径相对于工作区；绝对路径也必须落在工作区之内；`..` 逃逸一律拒绝；
// 经符号链接指向工作区之外的路径也拒绝（例如工作区里一个指向 ~/.ssh 的链接）。
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
	if !within(workspace, abs) {
		return "", fmt.Errorf("path %q is outside the workspace (file tools can only access the workspace; use shell to read files elsewhere, or ask the user to change the workspace)", p)
	}
	// 再按符号链接解析后的真实路径检查一次。工作区本身也要解析（例如 macOS 的 /tmp → /private/tmp）。
	realWS, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("workspace: %v", err)
	}
	real, err := evalExisting(abs)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %v", p, err)
	}
	if !within(realWS, real) {
		return "", fmt.Errorf("path %q is outside the workspace (via symlink)", p)
	}
	return abs, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// evalExisting 解析路径中已存在部分的符号链接，再接上尚不存在的部分（write_file 要创建的新文件）。
func evalExisting(p string) (string, error) {
	var rest []string
	for {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(append([]string{real}, rest...)...), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		// 路径本身存在、只是解析失败：这是一个悬空的符号链接。不能把它当作"尚不存在的文件"放行，
		// 否则 write_file 会顺着它在工作区之外创建目标文件。
		if _, lerr := os.Lstat(p); lerr == nil {
			return "", fmt.Errorf("dangling symlink %s", p)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = parent
	}
}

// truncate 截断过长的输出，并明确告诉模型"被截断了"，避免它误以为看到了全部内容。
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	// 退到字符边界再截断，避免把一个多字节字符（例如汉字）切成两半。
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n...[truncated %d bytes]", len(s)-cut)
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
