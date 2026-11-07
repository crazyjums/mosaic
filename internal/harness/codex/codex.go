package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/harness"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// Type 是厂商配置档的 type，也是引擎名。
const Type = "codex"

func init() {
	// 配置档需要一个 Provider 才能进目录；真正的工作由 Harness 完成，Provider 只提供名字和模型列表。
	provider.Register(Type, func(cfg provider.ProfileConfig) (provider.Provider, error) {
		return stubProvider{name: cfg.Name}, nil
	})
}

type stubProvider struct{ name string }

func (p stubProvider) Name() string { return p.name }
func (p stubProvider) Stream(context.Context, provider.Request, func(provider.Delta)) error {
	return errors.New("codex 由 Codex CLI 自己驱动，不能当作普通模型厂商调用")
}

// Codex 是 harness.Harness 的实现。
type Codex struct {
	// Command 是默认的可执行文件；配置档的 options.command 优先。空表示在 PATH 里找 "codex"。
	Command string
}

func New() *Codex { return &Codex{} }

func (*Codex) Name() string { return Type }

// 启动、停止的时限。
const (
	handshakeTimeout = 30 * time.Second
	interruptGrace   = 5 * time.Second
	exitGrace        = 3 * time.Second
)

// resolveCommand 找 codex 可执行文件，找不到时给出能照着做的提示。
func (c *Codex) resolveCommand(opts map[string]any) (string, error) {
	name := c.Command
	if v, ok := opts["command"].(string); ok && strings.TrimSpace(v) != "" {
		name = strings.TrimSpace(v)
	}
	if name == "" {
		name = "codex"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("没有找到 Codex 命令 %q。请先安装 Codex CLI（npm install -g @openai/codex）并执行 codex login 登录，或在厂商配置档的选项里指定 command", name)
	}
	return path, nil
}

// policy 把 Mosaic 的权限模式映射成 Codex 的审批策略和沙箱。
//
//	readonly    → 只读沙箱；其余操作按需询问
//	default     → 工作区可写；越界或要联网执行时询问（Codex 的 on-request）
//	acceptEdits → 同 default：Codex 没有“改文件免问、执行要问”的拆分，命令和越界写仍会询问
//	auto        → 不询问、不设沙箱：与 Mosaic 的自动模式（放行一切）一致
func policy(mode permission.Mode) (approval, sandbox string, sandboxPolicy map[string]any) {
	switch mode {
	case permission.ModeReadOnly:
		return "on-request", "read-only", map[string]any{"type": "readOnly"}
	case permission.ModeAuto:
		return "never", "danger-full-access", map[string]any{"type": "dangerFullAccess"}
	default:
		return "on-request", "workspace-write", map[string]any{"type": "workspaceWrite"}
	}
}

// tailBuffer 只保留 stderr 的最后一小段，出错时拼进错误信息。
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(bytes.ToValidUTF8(t.buf, nil)))
}

// Run 实现 harness.Harness。
func (c *Codex) Run(ctx context.Context, req harness.Request) harness.Result {
	res := harness.Result{Resume: req.Resume}
	fail := func(err error) harness.Result {
		res.Reason, res.Err = event.TurnError, err
		return res
	}

	path, err := c.resolveCommand(req.Options)
	if err != nil {
		return fail(err)
	}

	cmd := exec.Command(path, "app-server")
	cmd.Dir = req.Workspace
	cmd.Env = os.Environ()
	stderr := &tailBuffer{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fail(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	if err := cmd.Start(); err != nil {
		return fail(fmt.Errorf("启动 Codex 失败: %w", err))
	}
	return c.session(ctx, req, res, cmd, stdin, stdout, stderr)
}

func (c *Codex) session(ctx context.Context, req harness.Request, res harness.Result, cmd *exec.Cmd, stdin io.WriteCloser, stdout io.Reader, stderr *tailBuffer) harness.Result {
	tr := newTranslator(req.Model, Type, req.Emit)
	cl := newClient(stdin)
	turnDone := make(chan struct{})
	var doneOnce sync.Once
	cl.onNotify = func(method string, params json.RawMessage) {
		if tr.handle(method, params) {
			doneOnce.Do(func() { close(turnDone) })
		}
	}
	cl.onRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		return handleServerRequest(ctx, req, tr, method, params)
	}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = cl.serve(ctx, stdout)
	}()

	// 收尾：关 stdin 让 app-server 退出，等不到就杀掉，并等读循环结束。幂等。
	// 必须在读取 translator 的最终状态（messages、用量）之前调用：读循环还活着时它仍会改 translator，
	// 并发读写会弄坏要存进历史的内容。
	var stopOnce sync.Once
	stopAll := func() {
		stopOnce.Do(func() {
			_ = stdin.Close()
			select {
			case <-serveDone:
			case <-time.After(exitGrace):
				_ = cmd.Process.Kill()
				<-serveDone
			}
			_ = cmd.Wait()
		})
	}
	defer stopAll()

	failWith := func(err error) harness.Result {
		stopAll()
		if tail := stderr.String(); tail != "" {
			err = fmt.Errorf("%w\n%s", err, lastLines(tail, 6))
		}
		res.Reason, res.Err = event.TurnError, err
		res.Messages = tr.finish("error")
		res.Usage = tr.usage
		return res
	}

	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	var initRes struct {
		UserAgent string `json:"userAgent"`
	}
	if err := cl.call(hctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "mosaic", "title": "Mosaic", "version": "1"}}, &initRes); err != nil {
		return failWith(fmt.Errorf("Codex 握手失败: %w", err))
	}
	_ = cl.notify("initialized", nil)

	// 没登录时 Codex 会把请求重试近一分钟才报错；先问一句账号状态，该提示的话立刻提示。
	var acct struct {
		Account      json.RawMessage `json:"account"`
		RequiresAuth bool            `json:"requiresOpenaiAuth"`
	}
	if err := cl.call(hctx, "account/read", map[string]any{"refreshToken": false}, &acct); err == nil {
		if acct.RequiresAuth && (len(acct.Account) == 0 || string(acct.Account) == "null") {
			return failWith(errors.New("Codex 还没有登录。请在终端执行 codex login（ChatGPT 账号或 API Key），然后重试"))
		}
	}

	approval, sandbox, sandboxPolicy := policy(req.PermissionMode)
	threadID, err := openThread(hctx, cl, req, approval, sandbox)
	if err != nil {
		return failWith(err)
	}
	res.Resume = threadID

	turnParams := map[string]any{
		"threadId":       threadID,
		"input":          []any{map[string]any{"type": "text", "text": req.Text}},
		"cwd":            req.Workspace,
		"approvalPolicy": approval,
		"sandboxPolicy":  sandboxPolicy,
	}
	if m := modelOf(req.Model); m != "" {
		turnParams["model"] = m
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := cl.call(hctx, "turn/start", turnParams, &started); err != nil {
		return failWith(fmt.Errorf("开始对话失败: %w", err))
	}
	cancel() // 握手阶段结束，后面的等待只受 ctx 控制

	select {
	case <-turnDone:
	case <-serveDone:
		// 进程在这一轮结束前就退出了。
		select {
		case <-turnDone:
		default:
			return failWith(errors.New("Codex 进程意外退出"))
		}
	case <-ctx.Done():
		// 用户点了停止：先请 Codex 中断（它会把已有的内容整理好并发 turn/completed），宽限期内没结束就强制收尾。
		ictx, icancel := context.WithTimeout(context.Background(), interruptGrace)
		_ = cl.call(ictx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": started.Turn.ID}, nil)
		icancel()
		select {
		case <-turnDone:
		case <-time.After(interruptGrace):
		}
		stopAll() // 宽限期过了 Codex 还在发通知也要先停掉读循环，再整理历史
		res.Reason = event.TurnCancelled
		res.Messages = tr.finish("cancelled")
		res.Usage = tr.usage
		return res
	}

	stopAll() // 这一轮已经结束；先停读循环再读 translator 的最终状态
	switch tr.status {
	case "interrupted":
		res.Reason = event.TurnCancelled
		res.Messages = tr.finish("cancelled")
	case "failed":
		msg := tr.lastErr
		if msg == "" {
			msg = "Codex 报告这一轮失败了"
		}
		res.Reason, res.Err = event.TurnError, errors.New(msg)
		res.Messages = tr.finish("error")
	default:
		res.Reason = event.TurnCompleted
		res.Messages = tr.finish("end_turn")
	}
	res.Usage = tr.usage
	return res
}

// openThread 恢复保存的线程；没有或恢复失败（比如 Codex 那边清理过历史）就新开一个。
func openThread(ctx context.Context, cl *client, req harness.Request, approval, sandbox string) (string, error) {
	params := map[string]any{"cwd": req.Workspace, "approvalPolicy": approval, "sandbox": sandbox}
	if m := modelOf(req.Model); m != "" {
		params["model"] = m
	}
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if req.Resume != "" {
		rp := map[string]any{"threadId": req.Resume, "cwd": req.Workspace, "approvalPolicy": approval, "sandbox": sandbox}
		if m := modelOf(req.Model); m != "" {
			rp["model"] = m
		}
		if err := cl.call(ctx, "thread/resume", rp, &out); err == nil && out.Thread.ID != "" {
			return out.Thread.ID, nil
		}
		out.Thread.ID = ""
	}
	if err := cl.call(ctx, "thread/start", params, &out); err != nil {
		return "", fmt.Errorf("创建 Codex 线程失败: %w", err)
	}
	if out.Thread.ID == "" {
		return "", errors.New("Codex 没有返回线程 ID")
	}
	return out.Thread.ID, nil
}

// modelOf：空或 "default" 表示用 Codex 自己的默认模型（不覆盖）。
func modelOf(m string) string {
	if m == "" || m == "default" {
		return ""
	}
	return m
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
