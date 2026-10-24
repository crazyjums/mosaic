package openai

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

// 一段"录制"的 DeepSeek 风格流：先思考（reasoning_content），再文本，再分片到达的两个工具调用。
const recorded = `data: {"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"让我"}}]}

data: {"choices":[{"index":0,"delta":{"reasoning_content":"想想"}}]}

data: {"choices":[{"index":0,"delta":{"content":"好的，"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"list_dir","arguments":"{}"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}}

data: [DONE]

`

func TestStreamTranslation(t *testing.T) {
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("unexpected request %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, recorded)
	}))
	defer srv.Close()

	p, err := New(provider.ProfileConfig{Name: "ds", BaseURL: srv.URL + "/v1/", APIKey: "sk-test", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	var ds []provider.Delta
	err = p.Stream(context.Background(), provider.Request{
		Model:    "deepseek-chat",
		System:   "sys",
		Messages: []provider.Message{provider.UserText("hi")},
		Tools:    []provider.ToolSpec{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}, func(d provider.Delta) { ds = append(ds, d) })
	if err != nil {
		t.Fatal(err)
	}

	// 请求体检查。
	if gotBody.Model != "deepseek-chat" || !gotBody.Stream || gotBody.MaxTokens != 100 ||
		gotBody.Messages[0].Role != "system" || len(gotBody.Tools) != 1 || gotBody.StreamOptions == nil {
		t.Fatalf("请求体不符: %+v", gotBody)
	}

	thinking, text := "", ""
	var calls []*provider.ToolCall
	var usage *provider.Usage
	var stop provider.StopReason
	for _, d := range ds {
		switch d.Type {
		case provider.DeltaThinking:
			thinking += d.Text
		case provider.DeltaText:
			text += d.Text
		case provider.DeltaToolCall:
			calls = append(calls, d.ToolCall)
		case provider.DeltaUsage:
			usage = d.Usage
		case provider.DeltaStop:
			stop = d.StopReason
		}
	}
	if thinking != "让我想想" || text != "好的，" {
		t.Fatalf("thinking=%q text=%q", thinking, text)
	}
	if len(calls) != 2 || calls[0].ID != "call_a" || string(calls[0].Input) != `{"path":"a.txt"}` || calls[1].Name != "list_dir" {
		t.Fatalf("工具调用拼接错误: %+v %+v", calls[0], calls[1])
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 7 || usage.CacheReadTokens != 4 {
		t.Fatalf("usage: %+v", usage)
	}
	if stop != provider.StopToolUse || ds[len(ds)-1].Type != provider.DeltaStop {
		t.Fatalf("stop: %s", stop)
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer srv.Close()
	p, _ := New(provider.ProfileConfig{BaseURL: srv.URL, APIKey: "x"})
	err := p.Stream(context.Background(), provider.Request{}, func(provider.Delta) {})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("应返回可读错误: %v", err)
	}
}

func TestMissingKey(t *testing.T) {
	if _, err := New(provider.ProfileConfig{}); err == nil {
		t.Fatal("缺少密钥应报错")
	}
	if _, err := New(provider.ProfileConfig{Options: map[string]any{"allow_no_key": true}}); err != nil {
		t.Fatal("allow_no_key 时应允许无密钥")
	}
}

func TestConvertMessages(t *testing.T) {
	msgs := []provider.Message{
		provider.UserText("读 a.txt"),
		{Role: provider.RoleAssistant, Content: []provider.Block{
			{Type: provider.BlockThinking, Text: "想", Signature: "s"},
			{Type: provider.BlockText, Text: "好"},
			{Type: provider.BlockToolUse, ToolCallID: "c1", ToolName: "read_file", ToolInput: json.RawMessage(`{"path":"a.txt"}`)},
		}},
		{Role: provider.RoleUser, Content: []provider.Block{
			{Type: provider.BlockToolResult, ToolCallID: "c1", Text: "内容"},
		}},
		{Role: provider.RoleUser, Content: []provider.Block{
			{Type: provider.BlockText, Text: "看图"},
			{Type: provider.BlockImage, MediaType: "image/png", ImageData: "AAA"},
		}},
	}
	out := convertMessages("", msgs)
	if len(out) != 4 {
		t.Fatalf("应转换为 4 条消息: %+v", out)
	}
	if out[1].Content != "好" || out[1].ToolCalls[0].Function.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("assistant 转换错误（思考应被丢弃）: %+v", out[1])
	}
	if out[2].Role != "tool" || out[2].ToolCallID != "c1" {
		t.Fatalf("工具结果应转换为 role=tool: %+v", out[2])
	}
	parts, ok := out[3].Content.([]contentPart)
	if !ok || parts[1].ImageURL.URL != "data:image/png;base64,AAA" {
		t.Fatalf("图片应转换为 data URL: %+v", out[3])
	}
}

func TestNormalizeArgs(t *testing.T) {
	if string(normalizeArgs("")) != "{}" || string(normalizeArgs(`{"a":1}`)) != `{"a":1}` {
		t.Fatal("合法参数应原样返回")
	}
	if !json.Valid(normalizeArgs(`{"a":`)) {
		t.Fatal("非法参数应被包装为合法 JSON")
	}
}
