package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// 一段"录制"的 Messages API 流：思考（带签名）→ 文本 → 工具调用（参数分片）→ 加密思考。
const recorded = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":20,"cache_read_input_tokens":5,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"先看"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"目录"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG=="}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: ping
data: {"type":"ping"}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"好的"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"list_dir","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"pa"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"th\":\".\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"redacted_thinking","data":"ENCRYPTED"}}

event: content_block_stop
data: {"type":"content_block_stop","index":3}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}

`

func TestStreamTranslation(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("unexpected request: %s %v", r.URL.Path, r.Header)
		}
		if r.Header.Get("anthropic-beta") != fallbackBeta {
			t.Errorf("开启 refusal_fallback 时应带 beta 头")
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		io.WriteString(w, recorded)
	}))
	defer srv.Close()

	p, err := New(provider.ProfileConfig{Name: "claude", BaseURL: srv.URL, APIKey: "k",
		Options: map[string]any{"effort": "high", "refusal_fallback": true}})
	if err != nil {
		t.Fatal(err)
	}
	var ds []provider.Delta
	if err := p.Stream(context.Background(), provider.Request{
		Model: "claude-opus-5-5", System: "sys",
		Messages: []provider.Message{provider.UserText("hi")},
	}, func(d provider.Delta) { ds = append(ds, d) }); err != nil {
		t.Fatal(err)
	}

	// 请求体：adaptive thinking + summarized，effort，fallbacks，默认 max_tokens。
	th, _ := body["thinking"].(map[string]any)
	if th["type"] != "adaptive" || th["display"] != "summarized" {
		t.Fatalf("thinking 参数不符: %v", body["thinking"])
	}
	if oc, _ := body["output_config"].(map[string]any); oc["effort"] != "high" {
		t.Fatalf("effort: %v", body["output_config"])
	}
	if body["fallbacks"] != "default" || body["max_tokens"].(float64) != defaultMaxTokens || body["stream"] != true {
		t.Fatalf("body: %v", body)
	}
	if _, has := body["temperature"]; has {
		t.Fatal("不应发送 temperature")
	}

	var types []provider.DeltaType
	thinking, sig := "", ""
	for _, d := range ds {
		types = append(types, d.Type)
		switch d.Type {
		case provider.DeltaThinking:
			thinking += d.Text
		case provider.DeltaThinkingSignature:
			sig += d.Text
		case provider.DeltaToolCall:
			if d.ToolCall.ID != "toolu_1" || string(d.ToolCall.Input) != `{"path":"."}` {
				t.Fatalf("tool call: %+v %s", d.ToolCall, d.ToolCall.Input)
			}
		case provider.DeltaUsage:
			if d.Usage.InputTokens != 20 || d.Usage.OutputTokens != 42 || d.Usage.CacheReadTokens != 5 {
				t.Fatalf("usage: %+v", d.Usage)
			}
		case provider.DeltaStop:
			if d.StopReason != provider.StopToolUse {
				t.Fatalf("stop: %s", d.StopReason)
			}
		}
	}
	if thinking != "先看目录" || sig != "SIG==" {
		t.Fatalf("thinking=%q sig=%q", thinking, sig)
	}
	want := []provider.DeltaType{
		provider.DeltaThinking, provider.DeltaThinking, provider.DeltaThinkingSignature,
		provider.DeltaText, provider.DeltaToolCall, provider.DeltaRedactedThinking,
		provider.DeltaUsage, provider.DeltaStop,
	}
	if len(types) != len(want) {
		t.Fatalf("delta 序列\n got  %v\n want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("delta 序列\n got  %v\n want %v", types, want)
		}
	}
}

func TestStreamErrorEventAndTruncation(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"流内 error 事件", "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n", "Overloaded"},
		{"没有 message_stop 就断流", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{}}}\n\n", "before message_stop"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, c.body)
			}))
			defer srv.Close()
			p, _ := New(provider.ProfileConfig{BaseURL: srv.URL, APIKey: "k"})
			err := p.Stream(context.Background(), provider.Request{}, func(provider.Delta) {})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad thinking"}}`)
	}))
	defer srv.Close()
	p, _ := New(provider.ProfileConfig{BaseURL: srv.URL, APIKey: "k"})
	err := p.Stream(context.Background(), provider.Request{}, func(provider.Delta) {})
	if err == nil || !strings.Contains(err.Error(), "invalid_request_error") || !strings.Contains(err.Error(), "400") {
		t.Fatalf("应返回可读错误: %v", err)
	}
}

func TestConvertMessagesPreservesThinking(t *testing.T) {
	msgs := []provider.Message{
		provider.UserText("hi"),
		{Role: provider.RoleAssistant, Content: []provider.Block{
			{Type: provider.BlockThinking, Text: "有签名", Signature: "S"},
			{Type: provider.BlockThinking, Text: "无签名（来自其他厂商）"},
			{Type: provider.BlockRedactedThinking, Data: "ENC"},
			{Type: provider.BlockText, Text: ""},
			{Type: provider.BlockToolUse, ToolCallID: "t1", ToolName: "x"},
		}},
		{Role: provider.RoleUser, Content: []provider.Block{
			{Type: provider.BlockToolResult, ToolCallID: "t1", Text: "ok", IsError: true},
		}},
	}
	out := convertMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("messages: %+v", out)
	}
	a := out[1].Content
	if len(a) != 3 || a[0].Signature != "S" || a[1].Type != "redacted_thinking" || string(a[2].Input) != "{}" {
		t.Fatalf("assistant 块转换错误: %+v", a)
	}
	if r := out[2].Content[0]; r.Type != "tool_result" || r.ToolUseID != "t1" || !r.IsError {
		t.Fatalf("tool_result: %+v", r)
	}
}

// 只有签名、思考文本为空的块回传时也必须带 thinking 字段，否则 API 返回 400。
func TestThinkingFieldAlwaysPresent(t *testing.T) {
	out := convertMessages([]provider.Message{{Role: provider.RoleAssistant, Content: []provider.Block{
		{Type: provider.BlockThinking, Signature: "S"},
		{Type: provider.BlockText, Text: "hi"},
	}}})
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"thinking":""`) {
		t.Fatalf("thinking 字段缺失: %s", s)
	}
	if strings.Count(s, `"thinking":`) != 1 {
		t.Fatalf("thinking 字段应只出现一次: %s", s)
	}
}
