package mcp

import (
	"bufio"
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
)

// stdioTransport 启动一个子进程，通过它的标准输入输出收发 JSON-RPC 消息。
//
// MCP 的 stdio 传输规定：每条消息是一行 JSON（消息内不能有换行），stdout 只能输出协议消息，
// 日志写 stderr。我们保留 stderr 的最后一段，连接失败时附在错误里——这是排查"server 起不来"最有用的信息。
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tailBuffer

	wmu sync.Mutex // 串行化写入：两条消息交错写入会破坏行格式

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan message
	exitErr error // 进程退出的原因，closed 关闭后有效

	closed    chan struct{}
	closeOnce sync.Once
}

func startStdio(cfg ServerConfig) (*stdioTransport, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("mcp: stdio server has no command")
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	// 用户自己配的 server 沿用应用的环境；插件带来的只给白名单，见 BaseEnv。
	if IsPluginSource(cfg.Source) {
		cmd.Env = BaseEnv()
	} else {
		cmd.Env = os.Environ()
	}
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}
	prepareProc(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	t := &stdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		stderr:  &tailBuffer{max: 4096},
		pending: map[string]chan message{},
		closed:  make(chan struct{}),
	}
	cmd.Stderr = t.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", cfg.Command, err)
	}
	go t.readLoop(stdout)
	return t, nil
}

// readLoop 逐行读取 stdout，把响应分发给等待中的调用。stdout 结束意味着进程退出（或关闭了输出）。
func (t *stdioTransport) readLoop(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024) // 工具结果可能很大（例如整个文件内容）
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			// 有些 server 会把日志误写到 stdout。忽略而不是断开：断开会让整个 server 不可用。
			continue
		}
		switch {
		case m.isResponse():
			t.mu.Lock()
			ch := t.pending[string(m.ID)]
			delete(t.pending, string(m.ID))
			t.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		case m.isRequest():
			_ = t.write(replyTo(m))
		}
		// 其余是通知（日志、进度、列表变化），目前不处理。
	}
	err := t.cmd.Wait()
	if err == nil {
		err = errors.New("server exited")
	}
	if tail := strings.TrimSpace(t.stderr.String()); tail != "" {
		err = fmt.Errorf("%w; stderr: %s", err, tail)
	}
	t.mu.Lock()
	t.exitErr = err
	t.mu.Unlock()
	close(t.closed)
}

func (t *stdioTransport) write(m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err = t.stdin.Write(append(b, '\n'))
	return err
}

func (t *stdioTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	t.nextID++
	req, err := newRequest(t.nextID, method, params)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	ch := make(chan message, 1)
	t.pending[string(req.ID)] = ch
	t.mu.Unlock()
	forget := func() {
		t.mu.Lock()
		delete(t.pending, string(req.ID))
		t.mu.Unlock()
	}

	if err := t.write(req); err != nil {
		forget()
		return nil, t.deadErr(err)
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		forget()
		// 告诉 server 不用再做了（尽力而为；server 可以忽略）。
		_ = t.notify(context.Background(), "notifications/cancelled", map[string]any{"requestId": req.ID, "reason": "cancelled by client"})
		return nil, ctx.Err()
	case <-t.closed:
		return nil, t.deadErr(nil)
	}
}

func (t *stdioTransport) notify(_ context.Context, method string, params any) error {
	m := message{JSONRPC: "2.0", Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		m.Params = b
	}
	return t.write(m)
}

// deadErr 在进程已退出时返回退出原因（比 "broken pipe" 有用得多），否则返回 fallback。
func (t *stdioTransport) deadErr(fallback error) error {
	select {
	case <-t.closed:
		t.mu.Lock()
		defer t.mu.Unlock()
		return fmt.Errorf("mcp: %w", t.exitErr)
	case <-time.After(100 * time.Millisecond): // 写失败时进程往往正在退出，稍等一下拿到真正原因
		if fallback == nil {
			fallback = errors.New("mcp: connection closed")
		}
		return fallback
	}
}

func (t *stdioTransport) done() <-chan struct{} { return t.closed }

func (t *stdioTransport) err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.exitErr
}

// close 先关闭 stdin（规范建议的优雅退出方式），等不到退出再强制结束整个进程组。
func (t *stdioTransport) close() error {
	t.closeOnce.Do(func() {
		_ = t.stdin.Close()
		select {
		case <-t.closed:
		case <-time.After(2 * time.Second):
			killProc(t.cmd)
			select {
			case <-t.closed:
			case <-time.After(2 * time.Second):
			}
		}
	})
	return nil
}

// tailBuffer 只保留最后 max 字节，用于收集子进程的 stderr。
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
