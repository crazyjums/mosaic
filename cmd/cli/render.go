package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/crazyjums/myllmapp/internal/event"
)

// ANSI 颜色。设置了 NO_COLOR 环境变量（https://no-color.org）时全部为空串。
type palette struct{ dim, cyan, green, red, yellow, bold, reset string }

func newPalette() palette {
	if os.Getenv("NO_COLOR") != "" {
		return palette{}
	}
	return palette{dim: "\033[2m", cyan: "\033[36m", green: "\033[32m", red: "\033[31m", yellow: "\033[33m", bold: "\033[1m", reset: "\033[0m"}
}

// renderer 把事件渲染为终端文本。它是事件总线的一个订阅者，和前端的 store 是"同一件事的两种实现"。
type renderer struct {
	out io.Writer
	c   palette
	// section 记录当前正在输出的是思考还是正文，用来在两者切换时换行并打印标题。
	section string
	// echoUser 为 true 时打印用户消息（输入来自管道时终端上看不到输入内容）。
	echoUser bool
	// replaying 表示正在渲染重放的历史，此时总是打印用户消息。
	replaying bool
}

func (r *renderer) handle(ev event.Event) {
	c := r.c
	switch p := ev.Data.(type) {
	case event.TurnStart:
		r.replaying = p.Replay
	case event.UserMessage:
		// 交互模式下用户刚输入过，不再重复。
		if r.replaying || r.echoUser {
			fmt.Fprintf(r.out, "%s> %s%s\n", c.bold, p.Text, c.reset)
		}
	case event.MessageStart:
		r.section = ""
		fmt.Fprintf(r.out, "%s[step %d · %s/%s]%s\n", c.dim, p.Step, p.Provider, p.Model, c.reset)
	case event.ThinkingDelta:
		if r.section != "thinking" {
			r.section = "thinking"
			fmt.Fprintf(r.out, "%s💭 思考：%s", c.dim, c.reset)
		}
		if p.Redacted {
			fmt.Fprintf(r.out, "%s（思考内容已加密）%s", c.dim, c.reset)
			return
		}
		fmt.Fprintf(r.out, "%s%s%s", c.dim, p.Text, c.reset)
	case event.TextDelta:
		if r.section != "text" {
			if r.section != "" {
				fmt.Fprintln(r.out)
			}
			r.section = "text"
		}
		fmt.Fprint(r.out, p.Text)
	case event.Image:
		r.newline()
		fmt.Fprintf(r.out, "%s🖼  [图片 %s，%d 字节；在桌面端可以看到图片]%s\n", c.cyan, p.MediaType, len(p.Data)*3/4, c.reset)
	case event.ToolCall:
		r.newline()
		fmt.Fprintf(r.out, "%s⚙  调用工具 %s%s %s%s\n", c.cyan, c.bold, p.Name, compactJSON(p.Input), c.reset)
	case event.MessageEnd:
		r.newline()
	case event.PermissionRequest:
		r.newline()
		fmt.Fprintf(r.out, "%s⚠  需要确认 [%s] %s%s\n", c.yellow, p.Risk, p.Summary, c.reset)
	case event.PermissionResolved:
		color := c.green
		if p.Decision == "deny" {
			color = c.red
		}
		fmt.Fprintf(r.out, "%s   → %s%s\n", color, p.Decision, c.reset)
	case event.ToolResult:
		color, mark := c.green, "✓"
		if p.IsError {
			color, mark = c.red, "✗"
		}
		fmt.Fprintf(r.out, "%s%s  %s（%d ms）%s\n%s\n", color, mark, p.Name, p.DurationMS, c.reset, indent(head(p.Output, 8)))
	case event.ContextCompacted:
		fmt.Fprintf(r.out, "%s🗜  上下文已压缩：%d → %d tokens%s\n", c.dim, p.BeforeTokens, p.AfterTokens, c.reset)
	case event.Error:
		r.newline()
		fmt.Fprintf(r.out, "%s✗ [%s] %s%s\n", c.red, p.Code, p.Message, c.reset)
	case event.TurnEnd:
		r.newline()
		if p.Replay {
			r.replaying = false
			return
		}
		fmt.Fprintf(r.out, "%s── %s · %d 步 · 本轮 tokens 入 %d / 出 %d · 会话累计 %d / %d ──%s\n",
			c.dim, p.Reason, p.Steps, p.Usage.InputTokens, p.Usage.OutputTokens,
			p.SessionUsage.InputTokens, p.SessionUsage.OutputTokens, c.reset)
	}
}

func (r *renderer) newline() {
	if r.section == "thinking" || r.section == "text" {
		fmt.Fprintln(r.out)
	}
	r.section = ""
}

func compactJSON(b json.RawMessage) string {
	s := string(b)
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return strings.ReplaceAll(s, "\n", "\\n")
}

func head(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("…（共 %d 行）", len(lines)))
	}
	return strings.Join(lines, "\n")
}

func indent(s string) string {
	return "   " + strings.ReplaceAll(s, "\n", "\n   ")
}
