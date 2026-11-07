package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// 假的 `codex app-server`：测试二进制自己重新执行一遍，按环境变量里的场景说话。
// 协议的形状来自真实的 `codex app-server generate-json-schema`（0.167）和对真实进程的握手探测。

const fakeEnv = "MOSAIC_FAKE_CODEX"

func TestMain(m *testing.M) {
	if sc := os.Getenv(fakeEnv); sc != "" {
		fakeServer(sc)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fake struct {
	in  *bufio.Scanner
	out *json.Encoder
}

func (f *fake) send(v any) { _ = f.out.Encode(v) }

func (f *fake) notify(method string, params any) {
	f.send(map[string]any{"method": method, "params": params})
}

// read 读下一条消息；EOF 返回 nil。
func (f *fake) read() map[string]any {
	if !f.in.Scan() {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(f.in.Bytes(), &m)
	return m
}

func (f *fake) item(started bool, item map[string]any) {
	method := "item/completed"
	if started {
		method = "item/started"
	}
	f.notify(method, map[string]any{"threadId": "t1", "turnId": "u1", "startedAtMs": 1, "item": item})
}

func fakeServer(scenario string) {
	f := &fake{in: bufio.NewScanner(os.Stdin), out: json.NewEncoder(os.Stdout)}
	f.in.Buffer(make([]byte, 1<<20), 64<<20)
	var thread string
	for {
		m := f.read()
		if m == nil {
			return
		}
		method, _ := m["method"].(string)
		id := m["id"]
		switch method {
		case "initialize":
			f.send(map[string]any{"id": id, "result": map[string]any{"userAgent": "fake", "codexHome": "/tmp/x"}})
		case "initialized":
		case "account/read":
			acct := any(map[string]any{"type": "chatgpt", "email": "a@b.c", "planType": "plus"})
			if scenario == "unauth" {
				acct = nil
			}
			f.send(map[string]any{"id": id, "result": map[string]any{"account": acct, "requiresOpenaiAuth": true}})
		case "thread/start":
			thread = "t1"
			f.send(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": thread}}})
		case "thread/resume":
			p := m["params"].(map[string]any)
			if scenario == "resume-missing" {
				f.send(map[string]any{"id": id, "error": map[string]any{"code": -32600, "message": "no rollout found"}})
				continue
			}
			thread = p["threadId"].(string)
			f.send(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": thread}}})
		case "turn/start":
			p := m["params"].(map[string]any)
			f.send(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "u1", "items": []any{}, "status": "inProgress"}}})
			f.runTurn(scenario, thread, p)
		case "turn/interrupt":
			f.send(map[string]any{"id": id, "result": map[string]any{}})
			f.completeTurn("interrupted", "")
			return
		}
	}
}

func (f *fake) completeTurn(status, errMsg string) {
	turn := map[string]any{"id": "u1", "items": []any{}, "status": status}
	if errMsg != "" {
		turn["error"] = map[string]any{"message": errMsg}
	}
	f.notify("turn/completed", map[string]any{"threadId": "t1", "turn": turn})
}

func (f *fake) runTurn(scenario, thread string, params map[string]any) {
	f.notify("turn/started", map[string]any{"threadId": thread, "turn": map[string]any{"id": "u1", "items": []any{}, "status": "inProgress"}})
	input := params["input"].([]any)[0].(map[string]any)["text"].(string)

	switch scenario {
	case "crash":
		fmt.Fprintln(os.Stderr, "boom: something broke")
		os.Exit(3)
	case "exit-during-approval":
		// 发出审批请求后不等回答就退出：模拟 Codex 在用户还没点之前崩了。
		f.send(map[string]any{"id": "req-9", "method": "item/commandExecution/requestApproval", "params": map[string]any{
			"threadId": thread, "turnId": "u1", "itemId": "c9", "startedAtMs": 1, "command": "rm -rf x",
		}})
		os.Exit(0)
	case "interrupt":
		f.item(true, map[string]any{"type": "agentMessage", "id": "m1", "text": ""})
		f.notify("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": "u1", "itemId": "m1", "delta": "思考中"})
		f.waitForInterrupt()
		return
	case "failed":
		f.notify("error", map[string]any{"threadId": thread, "turnId": "u1", "willRetry": false, "error": map[string]any{"message": "额度用完了"}})
		f.completeTurn("failed", "额度用完了")
		return
	}

	// 通用脚本：思考 → 说话 → 跑命令（可能先审批）→ 再说一句 → 改文件 → 结束。
	f.item(true, map[string]any{"type": "reasoning", "id": "r1", "summary": []any{}, "content": []any{}})
	f.notify("item/reasoning/summaryTextDelta", map[string]any{"threadId": thread, "turnId": "u1", "itemId": "r1", "summaryIndex": 0, "delta": "先看看目录"})
	f.item(true, map[string]any{"type": "agentMessage", "id": "m1", "text": ""})
	f.notify("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": "u1", "itemId": "m1", "delta": "你好，"})
	f.notify("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": "u1", "itemId": "m1", "delta": "收到：" + input})
	f.item(false, map[string]any{"type": "agentMessage", "id": "m1", "text": "你好，收到：" + input})

	cmd := map[string]any{"type": "commandExecution", "id": "c1", "command": "ls -la", "cwd": "/work", "status": "inProgress", "commandActions": []any{}, "source": "agent"}
	decision := "accept"
	if scenario == "approval" || scenario == "approval-deny" || scenario == "approval-session" {
		f.send(map[string]any{"id": "req-1", "method": "item/commandExecution/requestApproval", "params": map[string]any{
			"threadId": thread, "turnId": "u1", "itemId": "c1", "startedAtMs": 1, "command": "ls -la", "cwd": "/work", "reason": "列目录",
		}})
		resp := f.read() // 用户的回答
		if r, ok := resp["result"].(map[string]any); ok {
			decision, _ = r["decision"].(string)
		}
		if resp == nil {
			return
		}
	}
	if decision == "decline" || decision == "cancel" {
		cmd["status"] = "declined"
		f.item(true, cmd)
		f.item(false, cmd)
		if decision == "cancel" {
			f.completeTurn("interrupted", "")
			return
		}
	} else {
		f.item(true, cmd)
		f.notify("item/commandExecution/outputDelta", map[string]any{"threadId": thread, "turnId": "u1", "itemId": "c1", "delta": "total 0\n"})
		done := map[string]any{}
		for k, v := range cmd {
			done[k] = v
		}
		done["status"] = "completed"
		done["exitCode"] = 0
		done["aggregatedOutput"] = "total 0\nfile.txt\n"
		done["durationMs"] = 12
		f.item(false, done)
	}
	f.notify("thread/tokenUsage/updated", map[string]any{"threadId": thread, "turnId": "u1", "tokenUsage": map[string]any{
		"last":  map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 20, "reasoningOutputTokens": 5, "totalTokens": 120},
		"total": map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 20, "reasoningOutputTokens": 5, "totalTokens": 120},
	}})

	f.item(true, map[string]any{"type": "agentMessage", "id": "m2", "text": ""})
	f.notify("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": "u1", "itemId": "m2", "delta": "决定=" + decision + "；线程=" + thread})
	f.item(false, map[string]any{"type": "agentMessage", "id": "m2", "text": "决定=" + decision + "；线程=" + thread})
	f.item(true, map[string]any{"type": "fileChange", "id": "f1", "status": "inProgress", "changes": []any{map[string]any{"path": "/work/a.txt", "kind": map[string]any{"type": "add"}, "diff": "+hi"}}})
	f.item(false, map[string]any{"type": "fileChange", "id": "f1", "status": "completed", "changes": []any{map[string]any{"path": "/work/a.txt", "kind": map[string]any{"type": "add"}, "diff": "+hi"}}})
	f.completeTurn("completed", "")
}

func (f *fake) waitForInterrupt() {
	for {
		m := f.read()
		if m == nil {
			return
		}
		if m["method"] == "turn/interrupt" {
			f.send(map[string]any{"id": m["id"], "result": map[string]any{}})
			f.completeTurn("interrupted", "")
			return
		}
	}
}

var _ = strings.TrimSpace
