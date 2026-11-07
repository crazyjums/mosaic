package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// client 是 app-server 的 JSON-RPC 客户端（一行一条消息）。
//
// 不复用 internal/rpc.Peer 的原因：Peer 为每条入站消息开一个 goroutine，消息处理顺序不定；
// 这里的通知是流式输出（文本增量、命令输出），必须按到达顺序处理。所以通知在读循环里同步处理，
// 只有需要等用户回答的请求（审批）才放到 goroutine 里。
type client struct {
	w   io.Writer
	wmu sync.Mutex

	nextID  atomic.Int64
	pmu     sync.Mutex
	pending map[string]chan rpcMessage
	closed  bool

	// onNotify 按到达顺序同步调用，处理要快。
	onNotify func(method string, params json.RawMessage)
	// onRequest 处理服务端的请求，在独立的 goroutine 里调用；返回值作为回应。
	onRequest func(ctx context.Context, method string, params json.RawMessage) (any, error)
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("codex: %s (%d)", e.Message, e.Code) }

// 单条消息上限：线程历史、命令输出可能很大。
const maxMessage = 64 << 20

var errClosed = errors.New("codex: app-server 连接已断开")

func newClient(w io.Writer) *client {
	return &client{w: w, pending: map[string]chan rpcMessage{}}
}

func (c *client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}

// notify 发一个通知。
func (c *client) notify(method string, params any) error {
	m := map[string]any{"method": method}
	if params != nil {
		m["params"] = params
	}
	return c.write(m)
}

// call 发一个请求并等回应。ctx 取消时立即返回。
func (c *client) call(ctx context.Context, method string, params any, result any) error {
	id := strconv.FormatInt(c.nextID.Add(1), 10)
	ch := make(chan rpcMessage, 1)
	c.pmu.Lock()
	if c.closed {
		c.pmu.Unlock()
		return errClosed
	}
	c.pending[id] = ch
	c.pmu.Unlock()
	defer func() {
		c.pmu.Lock()
		delete(c.pending, id)
		c.pmu.Unlock()
	}()
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m, ok := <-ch:
		if !ok {
			return errClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	}
}

// serve 读到 EOF 为止。返回前让所有等待中的 call 返回 errClosed，并等进行中的请求处理完。
func (c *client) serve(ctx context.Context, r io.Reader) error {
	var wg sync.WaitGroup
	defer func() {
		c.pmu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.pmu.Unlock()
		wg.Wait()
	}()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxMessage)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m rpcMessage
		if json.Unmarshal(line, &m) != nil {
			continue // 不是 JSON 的行（比如启动时的日志）直接忽略
		}
		hasID := len(m.ID) > 0 && string(m.ID) != "null"
		switch {
		case m.Method != "" && !hasID:
			if c.onNotify != nil {
				c.onNotify(m.Method, m.Params)
			}
		case m.Method != "" && hasID:
			wg.Add(1)
			go func(m rpcMessage) {
				defer wg.Done()
				c.answer(ctx, m)
			}(m)
		case hasID:
			var id string
			if json.Unmarshal(m.ID, &id) != nil {
				id = string(m.ID) // 数字 id
			}
			c.pmu.Lock()
			ch := c.pending[id]
			c.pmu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
	return sc.Err()
}

func (c *client) answer(ctx context.Context, m rpcMessage) {
	resp := map[string]any{"id": m.ID}
	if c.onRequest == nil {
		resp["error"] = map[string]any{"code": -32601, "message": "unsupported: " + m.Method}
	} else if res, err := c.onRequest(ctx, m.Method, m.Params); err != nil {
		code := -32000
		var re *rpcError
		if errors.As(err, &re) {
			code = re.Code
		}
		resp["error"] = map[string]any{"code": code, "message": err.Error()}
	} else {
		resp["result"] = res
	}
	// id 是原样回显的 RawMessage（数字或字符串都行）。
	_ = c.write(resp)
}
