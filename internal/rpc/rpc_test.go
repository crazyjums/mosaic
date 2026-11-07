package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type sample struct{ peer *Peer }

type Info struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

func (s *sample) Hello(name string, n int) Info { return Info{Name: "hi " + name, N: n} }
func (s *sample) Fail() error                   { return errors.New("出错了") }
func (s *sample) Maybe(ok bool) (string, error) {
	if !ok {
		return "", errors.New("不行")
	}
	return "行", nil
}
func (s *sample) Nothing()                     {}
func (s *sample) Boom() string                 { panic("炸了") }
func (s *sample) WithCallback(f func()) string { return "不应被收录" }
func (s *sample) Lifecycle()                   {}

// Ask 反过来请对方回答一个问题（模拟“请壳弹对话框”）。
func (s *sample) Ask(q string) (string, error) {
	var ans string
	err := s.peer.Call(context.Background(), "host.ask", map[string]string{"q": q}, &ans)
	return ans, err
}

// pair 建两端：server 处理 sample 的方法，client 回答 host.ask 并收集通知。
func pair(t *testing.T) (server, client *Peer, notes chan string, stop func()) {
	t.Helper()
	c2s, cw := io.Pipe()
	s2c, sw := io.Pipe()
	s := &sample{}
	methods := NewMethods(s, "Lifecycle")
	server = NewPeer(sw, methods.Call)
	s.peer = server
	notes = make(chan string, 10)
	client = NewPeer(cw, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "host.ask":
			var p struct{ Q string }
			_ = json.Unmarshal(params, &p)
			return "答：" + p.Q, nil
		default:
			notes <- method + " " + string(params)
			return nil, nil
		}
	})
	done := make(chan struct{}, 2)
	go func() { _ = server.Serve(context.Background(), c2s); done <- struct{}{} }()
	go func() { _ = client.Serve(context.Background(), s2c); done <- struct{}{} }()
	stop = func() {
		cw.Close()
		<-done
		sw.Close()
		<-done
	}
	return server, client, notes, stop
}

func TestCallMethods(t *testing.T) {
	_, c, _, stop := pair(t)
	defer stop()
	ctx := context.Background()

	var info Info
	if err := c.Call(ctx, "Hello", []any{"小明", 3}, &info); err != nil || info != (Info{"hi 小明", 3}) {
		t.Fatalf("%+v %v", info, err)
	}
	// 少给的参数按零值处理（和 Wails 一致）。
	if err := c.Call(ctx, "Hello", []any{"x"}, &info); err != nil || info.N != 0 {
		t.Fatalf("%+v %v", info, err)
	}
	var re *Error
	if err := c.Call(ctx, "Hello", []any{"x", 1, 2}, nil); !errors.As(err, &re) || re.Code != CodeInvalidParams {
		t.Fatalf("多给参数应报错: %v", err)
	}
	if err := c.Call(ctx, "Hello", []any{123, "x"}, nil); !errors.As(err, &re) || re.Code != CodeInvalidParams {
		t.Fatalf("类型不对应报错: %v", err)
	}
	if err := c.Call(ctx, "Fail", nil, nil); err == nil || err.Error() != "出错了" {
		t.Fatalf("方法返回的错误文本要原样传回: %v", err)
	}
	var s string
	if err := c.Call(ctx, "Maybe", []any{true}, &s); err != nil || s != "行" {
		t.Fatal(s, err)
	}
	if err := c.Call(ctx, "Maybe", []any{false}, &s); err == nil || err.Error() != "不行" {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "Nothing", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "Boom", nil, nil); !errors.As(err, &re) || re.Code != CodeInternal || !strings.Contains(re.Message, "炸了") {
		t.Fatalf("panic 应变成错误回应，后端不退出: %v", err)
	}
	for _, name := range []string{"Lifecycle", "WithCallback", "nope"} {
		if err := c.Call(ctx, name, nil, nil); !errors.As(err, &re) || re.Code != CodeMethodNotFound {
			t.Fatalf("%s 不应可调用: %v", name, err)
		}
	}
	// panic 之后还能继续调用。
	if err := c.Call(ctx, "Hello", []any{"y", 1}, &info); err != nil {
		t.Fatal(err)
	}
}

func TestReverseCallAndNotify(t *testing.T) {
	srv, c, notes, stop := pair(t)
	defer stop()
	var ans string
	// 前端调用 Ask → 后端在处理过程中反过来请壳回答 → 拿到答案后再回给前端。
	if err := c.Call(context.Background(), "Ask", []any{"选哪个目录"}, &ans); err != nil || ans != "答：选哪个目录" {
		t.Fatalf("%q %v", ans, err)
	}
	if err := srv.Notify("event", map[string]any{"name": "agent:event", "data": map[string]int{"seq": 1}}); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-notes:
		if n != `event {"data":{"seq":1},"name":"agent:event"}` {
			t.Fatalf("%q", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没收到通知")
	}
}

// 对方退出（stdin EOF）时，等待中的反向请求立刻返回，Serve 也能结束——不会卡死、不留孤儿 goroutine。
func TestCloseUnblocksPendingCalls(t *testing.T) {
	c2s, cw := io.Pipe()
	out := io.Discard
	p := NewPeer(out, nil)
	served := make(chan struct{})
	go func() { _ = p.Serve(context.Background(), c2s); close(served) }()
	errc := make(chan error, 1)
	go func() { errc <- p.Call(context.Background(), "host.ask", nil, nil) }()
	time.Sleep(50 * time.Millisecond)
	cw.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("连接断开后反向请求应立即返回")
	}
	<-served
	if err := p.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("断开后再调用应返回 ErrClosed: %v", err)
	}
}

func TestBadInput(t *testing.T) {
	r, w := io.Pipe()
	var buf strings.Builder
	p := NewPeer(&lockedWriter{w: &buf}, NewMethods(&sample{}).Call)
	done := make(chan struct{})
	go func() { _ = p.Serve(context.Background(), r); close(done) }()
	_, _ = io.WriteString(w, "not json\n{\"jsonrpc\":\"2.0\"}\n\n{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"Hello\",\"params\":{\"a\":1}}\n")
	w.Close()
	<-done
	got := buf.String()
	for _, want := range []string{`"code":-32700`, `"code":-32600`, `"id":7`, `"code":-32602`} {
		if !strings.Contains(got, want) {
			t.Errorf("输出里缺少 %s:\n%s", want, got)
		}
	}
	if names := NewMethods(&sample{}, "Lifecycle").Names(); strings.Join(names, ",") != "Ask,Boom,Fail,Hello,Maybe,Nothing" {
		t.Fatalf("%v", names)
	}
}

type lockedWriter struct{ w *strings.Builder }

func (l *lockedWriter) Write(b []byte) (int, error) { return l.w.Write(b) }
