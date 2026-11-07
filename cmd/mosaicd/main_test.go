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
