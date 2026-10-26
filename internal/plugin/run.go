package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/sysproc"
)

// 插件声明的状态检查和操作按钮运行的命令：在插件目录里、用白名单环境变量、有超时和输出上限，不经过 shell。
const (
	statusTimeout = 15 * time.Second
	actionTimeout = 120 * time.Second
	maxRunOutput  = 100 << 10
)

// runResult 是一次命令运行的结果。
type runResult struct {
	Output   string
	ExitCode int
	// Err 是没能正常运行的原因（找不到程序、超时、被取消）；程序自己以非 0 退出不算，看 ExitCode。
	Err error
}

// runCommand 运行 argv（已展开变量），把标准输出和标准错误合并返回。
func runCommand(ctx context.Context, dir string, argv []string, extraEnv []string, timeout time.Duration) runResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(mcp.BaseEnv(), extraEnv...)
	sysproc.HideConsole(cmd)
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	w := &capWriter{w: &out, left: maxRunOutput}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	res := runResult{Output: strings.ToValidUTF8(out.String(), "�")}
	if w.truncated {
		res.Output += "\n…（输出太长，已截断）"
	}
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		// 超时或被取消：进程是被我们杀掉的，退出码没有意义，也不要当作命令自己失败了。
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res.Err = fmt.Errorf("运行超时，已停止（最多 %d 秒）", int(timeout.Seconds()))
		} else {
			res.Err = errors.New("已取消")
		}
	case err == nil:
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
	default:
		res.Err = fmt.Errorf("无法运行 %s：%v", argv[0], err)
	}
	return res
}

// capWriter 最多保留 left 字节，超出的丢弃（不返回错误，让命令继续跑完）。
type capWriter struct {
	w         *bytes.Buffer
	left      int
	truncated bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	if len(p) > c.left {
		c.w.Write(p[:c.left])
		c.left, c.truncated = 0, true
		return len(p), nil
	}
	c.w.Write(p)
	c.left -= len(p)
	return len(p), nil
}

// jsonPath 按点号路径（"a.b.c"）取 JSON 里的值，只支持对象的键；取不到返回 false。
func jsonPath(data []byte, path string) (any, bool) {
	var v any
	if err := json.Unmarshal(bytes.TrimSpace(data), &v); err != nil {
		return nil, false
	}
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

// truthy 判断 JSON 值是否表示“正常”：true、非零数、非空字符串（"false"、"no"、"0"、"error" 除外）、非空对象/数组。
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "", "false", "no", "0", "error", "failed", "unauthenticated":
			return false
		}
		return true
	case nil:
		return false
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return false
}
