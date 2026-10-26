package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// TestMain 让测试二进制自己充当一个 stdio MCP server：环境变量 MCP_FAKE_SERVER=1 时不跑测试，
// 而是按协议读写标准输入输出。这样测试不依赖 node/python，也能在 Windows 上运行。
func TestMain(m *testing.M) {
	if os.Getenv("MCP_FAKE_SERVER") == "1" {
		runFakeStdioServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeStdioConfig(name string) ServerConfig {
	return ServerConfig{
		Name: name, Transport: TransportStdio,
		Command: os.Args[0],
		Env:     map[string]string{"MCP_FAKE_SERVER": "1"},
	}
}

// fakeHandle 是两种传输共用的 server 逻辑。返回 nil 表示不需要回复（通知）。
func fakeHandle(m message) *message {
	if !m.isRequest() {
		return nil
	}
	reply := func(v any) *message {
		b, _ := json.Marshal(v)
		return &message{JSONRPC: "2.0", ID: m.ID, Result: b}
	}
	switch m.Method {
	case "initialize":
		return reply(map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fake-server", "version": "1.2.3"},
		})
	case "tools/list":
		var p struct{ Cursor string }
		_ = json.Unmarshal(m.Params, &p)
		if p.Cursor == "" {
			return reply(map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "回显参数", "inputSchema": map[string]any{"type": "object"}}}, "nextCursor": "p2"})
		}
		return reply(map[string]any{"tools": []any{
			map[string]any{"name": "fail", "description": "总是失败"},
			map[string]any{"name": "dot.name", "description": "名字带点"},
			map[string]any{"name": "crash", "description": "让进程退出"},
		}})
	case "tools/call":
		var p struct {
			Name      string
			Arguments json.RawMessage
		}
		_ = json.Unmarshal(m.Params, &p)
		switch p.Name {
		case "echo":
			return reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(p.Arguments)}}})
		case "fail":
			return reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "坏了"}}, "isError": true})
		case "dot.name":
			return reply(map[string]any{"content": []any{map[string]any{"type": "image", "mimeType": "image/png", "data": "AAAA"}}})
		case "crash":
			fmt.Fprintln(os.Stderr, "fatal: something broke")
			os.Exit(3)
		}
	}
	return &message{JSONRPC: "2.0", ID: m.ID, Error: &rpcError{Code: codeMethodNotFound, Message: "no such method"}}
}

func runFakeStdioServer() {
	out := bufio.NewWriter(os.Stdout)
	send := func(m message) {
		b, _ := json.Marshal(m)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	fmt.Fprintln(out, "this is a log line that should have gone to stderr")
	out.Flush()
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var m message
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		// "ping_me"：先向客户端发一个 ping 请求，客户端必须回复，之后才返回结果。
		if m.Method == "tools/call" && strings.Contains(string(m.Params), `"ping_me"`) {
			send(message{JSONRPC: "2.0", ID: json.RawMessage(`"srv-1"`), Method: "ping"})
			for in.Scan() {
				var r message
				if json.Unmarshal(in.Bytes(), &r) == nil && string(r.ID) == `"srv-1"` && r.Result != nil {
					break
				}
			}
			b, _ := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": "pong-ok"}}})
			send(message{JSONRPC: "2.0", ID: m.ID, Result: b})
			continue
		}
		if r := fakeHandle(m); r != nil {
			send(*r)
		}
	}
}

func TestStdioClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Connect(ctx, fakeStdioConfig("fake"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.ServerName != "fake-server" || c.ServerVersion != "1.2.3" {
		t.Fatalf("server info: %+v", c)
	}

	tools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 4 || tools[0].Name != "echo" || tools[1].Name != "fail" {
		t.Fatalf("应自动翻页取到全部工具: %+v %v", tools, err)
	}

	r, err := c.CallTool(ctx, "echo", json.RawMessage(`{"x":1}`))
	if err != nil || r.Text != `{"x":1}` || r.IsError {
		t.Fatalf("echo: %+v %v", r, err)
	}
	r, _ = c.CallTool(ctx, "fail", nil)
	if !r.IsError || r.Text != "坏了" {
		t.Fatalf("isError 应传递: %+v", r)
	}
	r, _ = c.CallTool(ctx, "dot.name", nil)
	if !strings.Contains(r.Text, "image content omitted") {
		t.Fatalf("非文本内容应给出占位: %+v", r)
	}
	r, err = c.CallTool(ctx, "ping_me", nil)
	if err != nil || r.Text != "pong-ok" {
		t.Fatalf("应响应服务器的 ping: %+v %v", r, err)
	}
	if _, err := c.t.call(ctx, "resources/list", nil); err == nil || !strings.Contains(err.Error(), "-32601") {
		t.Fatalf("JSON-RPC 错误应返回: %v", err)
	}

	// 进程退出：正在进行的调用返回错误，错误里带上 stderr。
	_, err = c.CallTool(ctx, "crash", nil)
	if err == nil || !strings.Contains(err.Error(), "something broke") {
		t.Fatalf("应报告进程退出原因: %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done 应关闭")
	}
}

func TestStdioBadCommand(t *testing.T) {
	_, err := Connect(context.Background(), ServerConfig{Name: "x", Transport: TransportStdio, Command: "definitely-not-a-real-command-xyz"})
	if err == nil {
		t.Fatal("命令不存在应报错")
	}
}

func TestHTTPClient(t *testing.T) {
	var mu sync.Mutex
	var deleted bool
	var badAuth, badSession int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			mu.Lock()
			badAuth++
			mu.Unlock()
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			mu.Lock()
			deleted = r.Header.Get("Mcp-Session-Id") == "sess-1"
			mu.Unlock()
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m message
		_ = json.Unmarshal(body, &m)
		if m.Method != "initialize" && (r.Header.Get("Mcp-Session-Id") != "sess-1" || r.Header.Get("MCP-Protocol-Version") != "2025-03-26") {
			mu.Lock()
			badSession++
			mu.Unlock()
		}
		resp := fakeHandle(m)
		if resp == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		b, _ := json.Marshal(resp)
		if m.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "sess-1")
		}
		// tools/list 用 SSE 回复：先发一条通知，再发响应，测试从流中挑出正确的消息。
		if m.Method == "tools/list" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	defer srv.Close()

	ctx := context.Background()
	if _, err := Connect(ctx, ServerConfig{Name: "h", Transport: TransportHTTP, URL: srv.URL}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("认证失败应报错: %v", err)
	}
	c, err := Connect(ctx, ServerConfig{Name: "h", Transport: TransportHTTP, URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer tok"}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 4 {
		t.Fatalf("list: %+v %v", tools, err)
	}
	r, err := c.CallTool(ctx, "echo", json.RawMessage(`{"a":"b"}`))
	if err != nil || r.Text != `{"a":"b"}` {
		t.Fatalf("call: %+v %v", r, err)
	}
	c.Close()
	mu.Lock()
	defer mu.Unlock()
	if badSession != 0 {
		t.Errorf("后续请求应带上会话 ID 和协议版本，有 %d 个没带", badSession)
	}
	if !deleted {
		t.Error("关闭时应发 DELETE 结束会话")
	}
}

func TestValidateAndQualifiedName(t *testing.T) {
	good := ServerConfig{Name: "git-hub_1", Transport: TransportStdio, Command: "npx"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ServerConfig{
		{Name: "has space", Transport: TransportStdio, Command: "x"},
		{Name: "a", Transport: TransportStdio},
		{Name: "a", Transport: TransportHTTP, URL: "ftp://x"},
		{Name: "a", Transport: "ws", URL: "ws://x"},
		{Name: "a", Transport: TransportStdio, Command: "x", Risk: "dangerous"},
	} {
		if bad.Validate() == nil {
			t.Errorf("应报错: %+v", bad)
		}
	}
	if q := QualifiedName("s", "a.b/c"); q != "mcp__s__a_b_c" {
		t.Fatal(q)
	}
	if q := QualifiedName("s", strings.Repeat("x", 100)); len(q) != 64 {
		t.Fatal(len(q))
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("等待超时")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestManager(t *testing.T) {
	reg := tool.NewRegistry()
	m := NewManager(reg)
	defer m.Close()
	statusOf := func(name string) ServerStatus {
		for _, s := range m.Statuses() {
			if s.Name == name {
				return s
			}
		}
		return ServerStatus{}
	}

	cfg := fakeStdioConfig("fake")
	cfg.Risk = permission.RiskRead
	m.Apply([]ServerConfig{cfg, {Name: "off", Transport: TransportStdio, Command: "x", Disabled: true}, {Name: "bad", Transport: TransportStdio, Command: "definitely-not-a-real-command-xyz"}})

	waitFor(t, func() bool { return statusOf("fake").State == StateConnected })
	st := statusOf("fake")
	if len(st.Tools) != 4 || st.ServerName != "fake-server" {
		t.Fatalf("status: %+v", st)
	}
	echo, ok := reg.Get("mcp__fake__echo")
	if !ok || echo.Spec().Risk != permission.RiskRead {
		t.Fatal("工具应注册，并使用配置的风险等级")
	}
	if _, ok := reg.Get("mcp__fake__dot_name"); !ok {
		t.Fatal("名字中的点应被替换")
	}
	if r := echo.Execute(context.Background(), tool.Call{Input: json.RawMessage(`{"k":2}`)}); r.Output != `{"k":2}` {
		t.Fatalf("执行: %+v", r)
	}
	if statusOf("off").State != StateDisabled {
		t.Fatal("禁用的 server 不连接")
	}
	waitFor(t, func() bool { return statusOf("bad").State == StateFailed })
	if statusOf("bad").Error == "" {
		t.Fatal("失败应带原因")
	}

	// 进程退出：工具被移除，状态变为失败；Reconnect 后恢复。
	crash, _ := reg.Get("mcp__fake__crash")
	crash.Execute(context.Background(), tool.Call{})
	waitFor(t, func() bool { return statusOf("fake").State == StateFailed })
	if _, ok := reg.Get("mcp__fake__echo"); ok {
		t.Fatal("断开后工具应移除")
	}
	if !strings.Contains(statusOf("fake").Error, "something broke") {
		t.Fatalf("应显示退出原因: %+v", statusOf("fake"))
	}
	if err := m.Reconnect("fake"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return statusOf("fake").State == StateConnected })

	// 配置没变的 server 不重连；删掉的断开并移除工具。
	m.Apply([]ServerConfig{cfg})
	if statusOf("fake").State != StateConnected {
		t.Fatal("配置未变不应重连")
	}
	m.Apply(nil)
	if len(m.Statuses()) != 0 {
		t.Fatal("应全部移除")
	}
	if _, ok := reg.Get("mcp__fake__echo"); ok {
		t.Fatal("删除 server 后工具应移除")
	}
}

// 按来源管理：Apply（用户的）和 ApplySource（插件的）互不影响。
func TestManagerSources(t *testing.T) {
	reg := tool.NewRegistry()
	m := NewManager(reg)
	defer m.Close()
	statusOf := func(name string) ServerStatus {
		for _, s := range m.Statuses() {
			if s.Name == name {
				return s
			}
		}
		return ServerStatus{}
	}

	m.Apply([]ServerConfig{fakeStdioConfig("mine")})
	if err := m.ApplySource("plugin:demo", []ServerConfig{fakeStdioConfig("demo")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return statusOf("mine").State == StateConnected && statusOf("demo").State == StateConnected })
	if statusOf("mine").Source != "" || statusOf("demo").Source != "plugin:demo" {
		t.Fatalf("状态里带来源: %+v %+v", statusOf("mine"), statusOf("demo"))
	}
	if names := func() []string {
		var n []string
		for _, s := range m.Statuses() {
			n = append(n, s.Name)
		}
		return n
	}(); !slices.Equal(names, []string{"mine", "demo"}) {
		t.Fatalf("用户的在前、插件的在后: %v", names)
	}

	// 设置页重新保存用户的列表（不含插件的 server）：插件的不受影响。
	m.Apply([]ServerConfig{fakeStdioConfig("mine")})
	m.Apply(nil)
	if statusOf("demo").State != StateConnected {
		t.Fatal("用户的列表变化不应断开插件的 server")
	}
	if _, ok := reg.Get("mcp__demo__echo"); !ok {
		t.Fatal("插件的工具应还在")
	}
	if statusOf("mine").Name != "" {
		t.Fatal("用户的 server 应已移除")
	}

	// 前端传来的 source 字段会被覆盖，不能冒充插件。
	spoof := fakeStdioConfig("spoof")
	spoof.Source = "plugin:demo"
	m.Apply([]ServerConfig{spoof})
	waitFor(t, func() bool { return statusOf("spoof").State == StateConnected })
	if statusOf("spoof").Source != "" {
		t.Fatalf("用户列表里的 source 应被清掉: %+v", statusOf("spoof"))
	}

	// 名字被别的来源占用：新的不启动并报错，已有的不受影响。
	err := m.ApplySource("plugin:other", []ServerConfig{fakeStdioConfig("demo")})
	if err == nil || !strings.Contains(err.Error(), "demo") {
		t.Fatalf("应报告名字冲突: %v", err)
	}
	if statusOf("demo").Source != "plugin:demo" {
		t.Fatal("冲突不应改变已有 server")
	}

	// 停用插件：只摘掉它自己的。
	if err := m.ApplySource("plugin:demo", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("mcp__demo__echo"); ok {
		t.Fatal("停用后插件的工具应移除")
	}
	if statusOf("spoof").State != StateConnected {
		t.Fatal("用户的 server 不受影响")
	}
}

// 插件的子进程只拿到白名单环境变量和清单里声明的，拿不到应用的密钥；用户自己配的 server 仍然继承整个环境。
func TestPluginEnvWhitelist(t *testing.T) {
	t.Setenv("MOSAIC_TEST_SECRET", "s3cret")
	t.Setenv("PATH", os.Getenv("PATH"))
	env := BaseEnv()
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "MOSAIC_TEST_SECRET") {
		t.Fatalf("白名单外的变量不应继承: %v", env)
	}
	if !strings.Contains(joined, "PATH=") {
		t.Fatal("PATH 应继承")
	}
	if !IsPluginSource("plugin:x") || IsPluginSource("") || IsPluginSource("user") {
		t.Fatal("IsPluginSource")
	}
}
