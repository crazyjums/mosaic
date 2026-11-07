package rpc

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

// MaxMessage 是单条消息的上限：会话历史、图片附件可能很大，但不能让一条坏消息吃光内存。
const MaxMessage = 64 << 20

// 错误码（JSON-RPC 2.0 规定的几个，加上方法本身返回错误时用的 -32000）。
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeServer         = -32000
)

// Error 是 JSON-RPC 的错误对象。
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// message 覆盖请求、通知和回应三种消息：有 method 的是请求（有 id）或通知（没有 id），没有 method 的是回应。
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Handler 处理对方发来的一个请求或通知。params 是原样的 JSON；返回的 result 会被编码进回应（通知没有回应）。
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// Peer 是一端：既能处理对方发来的请求，也能向对方发请求（反向请求）和通知。
//
// 为什么是对等的而不是单纯的服务端：Go 后端要反过来请 Electron 主进程弹对话框、打开链接，
// 这些请求和前端的调用走同一条 stdio，所以两个方向都要支持。
type Peer struct {
	handler Handler

	wmu sync.Mutex
	enc *json.Encoder

	nextID  atomic.Int64
	pmu     sync.Mutex
	pending map[string]chan message
	closed  bool
}

// NewPeer 创建一端。handler 为 nil 时对方的所有请求都回“方法不存在”。
func NewPeer(w io.Writer, handler Handler) *Peer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Peer{handler: handler, enc: enc, pending: map[string]chan message{}}
}

func (p *Peer) write(m message) error {
	m.JSONRPC = "2.0"
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return p.enc.Encode(m) // Encode 自带换行：一行一条消息
}

// Notify 向对方发一条通知（不等回应）。
func (p *Peer) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return p.write(message{Method: method, Params: raw})
}

// ErrClosed 表示连接已经断开（对方退出、stdin 读到 EOF）。
var ErrClosed = errors.New("rpc: connection closed")

// Call 向对方发一个请求并等回应，结果解码进 result（可以为 nil）。ctx 取消时立即返回。
func (p *Peer) Call(ctx context.Context, method string, params any, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	// 反向请求的 id 带前缀，和对方自己发来的请求 id 不会混淆。
	id := "s" + strconv.FormatInt(p.nextID.Add(1), 10)
	ch := make(chan message, 1)
	p.pmu.Lock()
	if p.closed {
		p.pmu.Unlock()
		return ErrClosed
	}
	p.pending[id] = ch
	p.pmu.Unlock()
	defer func() {
		p.pmu.Lock()
		delete(p.pending, id)
		p.pmu.Unlock()
	}()
	idRaw, _ := json.Marshal(id)
	if err := p.write(message{ID: idRaw, Method: method, Params: raw}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
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

// Serve 从 r 逐行读取消息直到 EOF。每个请求在独立的 goroutine 里处理（和 Wails 的绑定一样可以并发），
// 回应按完成顺序写出，靠 id 配对。返回前让所有等待中的 Call 返回 ErrClosed，并等进行中的请求处理完。
func (p *Peer) Serve(ctx context.Context, r io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	defer func() {
		cancel()
		// 先让等待回应的反向请求返回：处理中的方法可能正卡在“等对方弹完对话框”，不先放开它们，下面的 Wait 会一直等。
		p.pmu.Lock()
		p.closed = true
		for id, ch := range p.pending {
			close(ch)
			delete(p.pending, id)
		}
		p.pmu.Unlock()
		wg.Wait()
	}()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), MaxMessage)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			_ = p.write(message{ID: json.RawMessage("null"), Error: &Error{Code: CodeParse, Message: "不是合法的 JSON：" + err.Error()}})
			continue
		}
		switch {
		case m.Method == "" && len(m.ID) > 0: // 对方对我们反向请求的回应
			var id string
			if json.Unmarshal(m.ID, &id) == nil {
				p.pmu.Lock()
				ch := p.pending[id]
				p.pmu.Unlock()
				if ch != nil {
					ch <- m
				}
			}
		case m.Method != "":
			wg.Add(1)
			go func(m message) {
				defer wg.Done()
				p.dispatch(ctx, m)
			}(m)
		default:
			_ = p.write(message{ID: json.RawMessage("null"), Error: &Error{Code: CodeInvalidRequest, Message: "消息既不是请求也不是回应"}})
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (p *Peer) dispatch(ctx context.Context, m message) {
	isCall := len(m.ID) > 0 && string(m.ID) != "null"
	var result any
	var err error
	if p.handler == nil {
		err = &Error{Code: CodeMethodNotFound, Message: "没有方法 " + m.Method}
	} else {
		result, err = safeCall(func() (any, error) { return p.handler(ctx, m.Method, m.Params) })
	}
	if !isCall {
		return
	}
	resp := message{ID: m.ID}
	if err != nil {
		var re *Error
		if !errors.As(err, &re) {
			re = &Error{Code: CodeServer, Message: err.Error()}
		}
		resp.Error = re
	} else {
		raw, merr := json.Marshal(result)
		if merr != nil {
			resp.Error = &Error{Code: CodeInternal, Message: "无法编码返回值：" + merr.Error()}
		} else {
			resp.Result = raw
		}
	}
	_ = p.write(resp)
}

// safeCall 把处理函数里的 panic 变成错误回应：一个方法出问题不能让整个后端退出。
func safeCall(f func() (any, error)) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &Error{Code: CodeInternal, Message: fmt.Sprintf("内部错误：%v", r)}
		}
	}()
	return f()
}
