package main

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/bootstrap"
	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/rpc"
	"github.com/crazyjums/myllmapp/internal/secret"
)

// 模拟 Electron 主进程：通过 stdio 调用绑定方法、收事件、回答权限确认和反向请求（弹对话框）。
// 这就是阶段 1 的 Electron 壳要做的全部事情，所以这条测试过了，换壳就只剩“窗口”的工作。
func TestServeOverStdio(t *testing.T) {
	cfg := config.Default()
	cfg.EnableDemo()
	cfg.DataDir = t.TempDir()
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Setenv(k, "")
	}
	built, err := bootstrap.Build(cfg, bootstrap.Options{Workspace: t.TempDir(), NoStore: true, Secrets: secret.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()

	toServer, clientOut := io.Pipe()
	toClient, serverOut := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- serve(context.Background(), built, "## [1.2.3] - 2026-01-01\n\n### 新增\n- x\n", toServer, serverOut)
	}()

	type raw = json.RawMessage
	events := make(chan map[string]any, 256)
	var hostCalls []string
	client := rpc.NewPeer(clientOut, func(_ context.Context, method string, params raw) (any, error) {
		switch method {
		case "event":
			var n struct {
				Name string         `json:"name"`
				Data map[string]any `json:"data"`
			}
			_ = json.Unmarshal(params, &n)
			if n.Name != "agent:event" {
				t.Errorf("事件名 %q", n.Name)
			}
			events <- n.Data
			return nil, nil
		case "host.openDirectory":
			hostCalls = append(hostCalls, method+" "+string(params))
			return "/chosen/dir", nil
		}
		return nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: method}
	})
	go func() { _ = client.Serve(context.Background(), toClient) }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 握手：协议版本和方法白名单；生命周期方法不在其中。
	var hello struct {
		Protocol int      `json:"protocol"`
		Methods  []string `json:"methods"`
	}
	if err := client.Call(ctx, "rpc.hello", nil, &hello); err != nil || hello.Protocol != ProtocolVersion {
		t.Fatalf("%+v %v", hello, err)
	}
	for _, want := range []string{"GetStatus", "NewSession", "SendToSession", "RespondPermission", "ChooseDirectory", "GetVersionHistory"} {
		if !slices.Contains(hello.Methods, want) {
			t.Errorf("方法列表里缺少 %s", want)
		}
	}
	for _, bad := range excluded {
		if slices.Contains(hello.Methods, bad) {
			t.Errorf("%s 不应暴露给前端", bad)
		}
	}

	var st struct {
		SessionID string `json:"session_id"`
		Provider  string `json:"provider"`
	}
	if err := client.Call(ctx, "NewSession", nil, &st); err != nil || st.SessionID == "" || st.Provider != "fake" {
		t.Fatalf("%+v %v", st, err)
	}

	// 跑完整的一轮：演示剧本里写文件、执行命令都要确认，这里像界面一样逐个“允许”。
	if err := client.Call(ctx, "SendToSession", []any{st.SessionID, "帮我做个演示", []any{}}, nil); err != nil {
		t.Fatal(err)
	}
	var types []string
	seen := map[float64]bool{}
	asked := 0
loop:
	for {
		select {
		case ev := <-events:
			typ, _ := ev["type"].(string)
			types = append(types, typ)
			// 事件由多个 goroutine 发布（agent 循环、回答权限确认的调用），写出顺序不一定等于 seq 顺序，
			// 前端靠 reorder.ts 按 seq 重排；这里只检查没有重复、没有丢失信封字段。
			if seq, _ := ev["seq"].(float64); seq <= 0 || seen[seq] {
				t.Errorf("seq 无效或重复: %v", ev)
			} else {
				seen[seq] = true
			}
			data, _ := ev["data"].(map[string]any)
			switch typ {
			case "permission_request":
				asked++
				if err := client.Call(ctx, "RespondPermission", []any{data["request_id"], "allow"}, nil); err != nil {
					t.Fatal(err)
				}
			case "turn_end":
				if data["reason"] != "completed" {
					t.Fatalf("这一轮应正常结束: %v", data)
				}
				break loop
			}
		case <-ctx.Done():
			t.Fatalf("超时，收到的事件：%v", types)
		}
	}
	if asked != 2 || !slices.Contains(types, "tool_result") || !slices.Contains(types, "text_delta") {
		t.Fatalf("asked=%d types=%v", asked, types)
	}

	// 反向请求：后端请壳弹“选择文件夹”，把壳返回的路径交回前端。
	var dir string
	if err := client.Call(ctx, "ChooseDirectory", []any{""}, &dir); err != nil || dir != "/chosen/dir" || len(hostCalls) != 1 {
		t.Fatalf("%q %v %v", dir, err, hostCalls)
	}
	// 方法的错误原样回给前端（和 Wails 里 Promise 被 reject 的文本一致）。
	if err := client.Call(ctx, "InstallUpdate", nil, nil); err == nil {
		t.Fatal("没检查过更新时安装应报错")
	}
	var hist []map[string]any
	if err := client.Call(ctx, "GetVersionHistory", nil, &hist); err != nil || len(hist) != 1 || hist[0]["version"] != "1.2.3" {
		t.Fatalf("%v %v", hist, err)
	}

	// 父进程退出（stdin EOF）：后端结束服务。
	clientOut.Close()
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdin 关闭后应退出")
	}
}

// 模拟 Electron 主进程提供“远程引擎”（Claude Code、pi 的 SDK 跑在 utilityProcess 里）：
// 壳注册引擎 → 前端选中引擎的配置档发消息 → Go 发 host.harness.run → 壳通过 harness.emit / harness.ask 回送过程，
// 审批走 Mosaic 自己的权限弹窗 → 壳返回结果。
func TestRemoteHarnessOverStdio(t *testing.T) {
	cfg := config.Default()
	cfg.EnableDemo()
	cfg.DataDir = t.TempDir()
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{Name: "cc", Type: "claudecode", Models: []string{"default"}, DefaultModel: "default"})
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Setenv(k, "")
	}
	built, err := bootstrap.Build(cfg, bootstrap.Options{Workspace: t.TempDir(), NoStore: true, Secrets: secret.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()

	toServer, clientOut := io.Pipe()
	toClient, serverOut := io.Pipe()
	go func() { _ = serve(context.Background(), built, "", toServer, serverOut) }()

	events := make(chan map[string]any, 256)
	var client *rpc.Peer
	var runSpec map[string]any
	client = rpc.NewPeer(clientOut, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "event":
			var n struct {
				Data map[string]any `json:"data"`
			}
			_ = json.Unmarshal(params, &n)
			events <- n.Data
			return nil, nil
		case "host.harness.run":
			var p struct {
				RunID string         `json:"run_id"`
				Spec  map[string]any `json:"spec"`
			}
			_ = json.Unmarshal(params, &p)
			runSpec = p.Spec
			emit := func(msg, typ string, data any) {
				if err := client.Call(ctx, "harness.emit", map[string]any{"run_id": p.RunID, "message_id": msg, "type": typ, "data": data}, nil); err != nil {
					t.Errorf("harness.emit %s: %v", typ, err)
				}
			}
			emit("m1", "message_start", map[string]any{"provider": "cc", "model": "default", "step": 1})
			emit("m1", "text_delta", map[string]any{"text": "好的，我来写文件"})
			emit("m1", "tool_call", map[string]any{"call_id": "t1", "name": "Write", "input": map[string]any{"file_path": "a.txt"}, "risk": "write"})
			var decision string
			if err := client.Call(ctx, "harness.ask", map[string]any{"run_id": p.RunID, "call_id": "t1", "tool": "Write", "risk": "write", "summary": "写入 a.txt"}, &decision); err != nil {
				t.Errorf("harness.ask: %v", err)
			}
			emit("", "tool_result", map[string]any{"call_id": "t1", "name": "Write", "output": "决定=" + decision})
			emit("m1", "message_end", map[string]any{"stop_reason": "tool_use"})
			// 轮结束后迟到的回调要被拒绝。
			return map[string]any{"reason": "completed", "resume": "cc-session-1", "usage": map[string]any{"input_tokens": 9, "output_tokens": 4}, "messages": []any{
				map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "好的，我来写文件"}}},
			}}, nil
		}
		return nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: method}
	})
	go func() { _ = client.Serve(context.Background(), toClient) }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var hello struct {
		Methods []string `json:"methods"`
	}
	if err := client.Call(ctx, "rpc.hello", nil, &hello); err != nil {
		t.Fatal(err)
	}
	// 渲染进程的白名单里不能有 harness.*：只有壳的主进程能驱动引擎。
	for _, m := range hello.Methods {
		if len(m) > 8 && m[:8] == "harness." {
			t.Fatalf("harness.* 不应暴露给渲染进程: %s", m)
		}
	}

	// 壳注册之前，选中这个配置档会得到“只在 Electron 版里可用”的提示；注册时不认识的引擎名被忽略。
	var reg struct {
		Registered []string `json:"registered"`
	}
	if err := client.Call(ctx, "harness.register", map[string]any{"engines": []string{"claudecode", "evil", "pi"}}, &reg); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reg.Registered, []string{"claudecode", "pi"}) {
		t.Fatalf("注册结果: %v", reg.Registered)
	}

	var st struct {
		SessionID string `json:"session_id"`
	}
	if err := client.Call(ctx, "NewSession", nil, &st); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, "SetModel", []any{"cc", "default"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, "SendToSession", []any{st.SessionID, "写个文件", []any{}}, nil); err != nil {
		t.Fatal(err)
	}
	var types []string
	var granted bool
loop:
	for {
		select {
		case ev := <-events:
			typ, _ := ev["type"].(string)
			types = append(types, typ)
			data, _ := ev["data"].(map[string]any)
			switch typ {
			case "permission_request":
				if data["tool"] != "Write" || data["call_id"] != "t1" {
					t.Errorf("权限请求: %v", data)
				}
				granted = true
				if err := client.Call(ctx, "RespondPermission", []any{data["request_id"], "allow_session"}, nil); err != nil {
					t.Fatal(err)
				}
			case "tool_result":
				if data["output"] != "决定=allow_session" {
					t.Errorf("引擎应收到用户的回答: %v", data)
				}
			case "turn_end":
				if data["reason"] != "completed" {
					t.Fatalf("这一轮应正常结束: %v", data)
				}
				break loop
			}
		case <-ctx.Done():
			t.Fatalf("超时，事件：%v", types)
		}
	}
	if !granted {
		t.Fatal("审批应经过 Mosaic 的权限弹窗")
	}
	for _, want := range []string{"turn_start", "user_message", "message_start", "text_delta", "tool_call", "permission_request", "permission_resolved", "tool_result", "message_end", "turn_end"} {
		if !slices.Contains(types, want) {
			t.Errorf("缺少事件 %s: %v", want, types)
		}
	}
	if runSpec["engine"] != "claudecode" || runSpec["text"] != "写个文件" || runSpec["session_id"] != st.SessionID {
		t.Fatalf("交给壳的请求: %v", runSpec)
	}

	// 壳不能伪造由内核掌握的事件，也不能给不存在的轮发东西。
	if err := client.Call(ctx, "harness.emit", map[string]any{"run_id": "nope", "type": "turn_end", "data": map[string]any{}}, nil); err == nil {
		t.Fatal("伪造事件应被拒绝")
	}
	if err := client.Call(ctx, "harness.bogus", nil, nil); err == nil {
		t.Fatal("未知的 harness 方法应报错")
	}
}
