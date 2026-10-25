package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/crazyjums/myllmapp/internal/sysproc"
)

// CommandStatusLine 是第一个真正实现的插件：运行用户配置的一条 shell 命令，把它的输出显示在状态栏上。
//
// 约定与 Claude Code 的 statusLine 相同，方便复用现成的脚本：
//   - 当前状态以 JSON 写到命令的标准输入（字段见 StatusInfo）；
//   - 标准输出的第一行就是状态栏文本，可以包含 ANSI 颜色码（前端会解析）。
type CommandStatusLine struct {
	Command string
	// Dir 是命令的工作目录，通常是当前工作区。
	Dir string
	// Timeout 为 0 时使用 DefaultStatusLineTimeout。状态栏会频繁刷新，脚本必须很快。
	Timeout time.Duration
}

const (
	DefaultStatusLineTimeout = 2 * time.Second
	// maxStatusLineRunes 截断过长的输出：状态栏只有一行，太长也显示不下。
	maxStatusLineRunes = 500
)

func (c CommandStatusLine) Render(ctx context.Context, info StatusInfo) (string, error) {
	if strings.TrimSpace(c.Command) == "" {
		return "", errors.New("statusline: empty command")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultStatusLineTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input, err := json.Marshal(info)
	if err != nil {
		return "", err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", c.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", c.Command)
	}
	sysproc.HideConsole(cmd) // 切换权限、用量变化时都会重新运行：在 Windows 上不能弹出控制台窗口
	cmd.Dir = c.Dir
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// 超时后 sh 被杀掉，但它启动的子进程可能还拿着输出管道；WaitDelay 到期后强制关闭管道，避免 Wait 一直卡住。
	cmd.WaitDelay = 500 * time.Millisecond

	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("statusline: command timed out after %s", timeout)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg != "" {
			return "", fmt.Errorf("statusline: %v: %s", err, msg)
		}
		return "", fmt.Errorf("statusline: %v", err)
	}
	line, _, _ := strings.Cut(strings.TrimRight(stdout.String(), "\r\n"), "\n")
	line = strings.TrimRight(line, "\r")
	if utf8.RuneCountInString(line) > maxStatusLineRunes {
		line = string([]rune(line)[:maxStatusLineRunes]) + "…"
	}
	return line, nil
}
