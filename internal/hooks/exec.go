package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/sysproc"
)

// execResult 是一次命令运行的原始结果，由 apply 解释。
type execResult struct {
	stdout, stderr string
	exitCode       int
	startErr       error
	timedOut       bool
	timeout        time.Duration
	cancelled      bool
}

// lookPath 可在测试里替换（模拟 Windows 上没有 sh）。
var lookPath = exec.LookPath

// exec 运行一条钩子：替换变量、只给白名单环境变量、限时、限制输出大小。
func (r *Runner) exec(ctx context.Context, h Hook, input map[string]any, cwd string) execResult {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
		if h.Event == core.HookUserPromptSubmit {
			timeout = defaultPromptTimout // 用户在等着发消息，不能卡太久
		}
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	res := execResult{timeout: timeout}

	vars := map[string]string{"CLAUDE_PLUGIN_ROOT": h.PluginRoot, "CLAUDE_PLUGIN_DATA": h.PluginData, "CLAUDE_PROJECT_DIR": cwd}
	expand := func(s string) string {
		for k, v := range vars {
			s = strings.ReplaceAll(s, "${"+k+"}", v)
		}
		return s
	}

	var name string
	var args []string
	if h.Args != nil {
		name = expand(h.Command)
		for _, a := range h.Args {
			args = append(args, expand(a))
		}
	} else {
		sh, err := lookPath("sh")
		if err != nil {
			if runtime.GOOS == "windows" {
				res.startErr = errors.New("这条钩子要用 sh 执行，这台电脑上没有找到 sh（可以安装 Git for Windows）")
			} else {
				res.startErr = errors.New("找不到 sh")
			}
			return res
		}
		// shell 形式不在这里替换变量：它们已经作为环境变量传入，交给 sh 自己展开。
		// 如果先把路径文本替换进命令，路径里的 $(…)、反引号等字符会变成 shell 语法被执行（即使原来加了引号）。
		name, args = sh, []string{"-c", h.Command}
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if st, err := os.Stat(cwd); err == nil && st.IsDir() {
		cmd.Dir = cwd
	}
	base := mcp.BaseEnv()
	if r.Env != nil {
		base = r.Env()
	}
	env := append([]string(nil), base...)
	for k, v := range vars {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	cmd.Env = env
	data, _ := json.Marshal(input)
	cmd.Stdin = bytes.NewReader(data)
	stdout, stderr := &capped{limit: maxOutputRunes * 4}, &capped{limit: maxOutputRunes * 4}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// 命令被杀掉之后，它启动的子进程可能还拿着输出管道；最多再等 2 秒就不管了，免得卡住这一轮。
	cmd.WaitDelay = 2 * time.Second
	sysproc.HideConsole(cmd)

	err := cmd.Run()
	res.stdout, res.stderr = trimRunes(stdout.String()), trimRunes(stderr.String())
	switch {
	case ctx.Err() != nil:
		res.cancelled = true
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		res.timedOut = true
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.exitCode = ee.ExitCode()
		} else {
			res.startErr = err
		}
	}
	return res
}

// capped 是有上限的缓冲区：超出的部分直接丢掉（钩子输出太多不该拖垮应用）。
type capped struct {
	bytes.Buffer
	limit int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.Len(); room > 0 {
		if len(p) > room {
			c.Buffer.Write(p[:room])
		} else {
			c.Buffer.Write(p)
		}
	}
	return len(p), nil
}

func trimRunes(s string) string {
	if r := []rune(s); len(r) > maxOutputRunes {
		return string(r[:maxOutputRunes]) + "…（输出过长，已截断）"
	}
	return s
}
