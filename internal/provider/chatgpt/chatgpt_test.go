package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/provider"
)

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

// recordingToken 同时实现 usageReporter，记录适配器上报的用量和额度错误。
type recordingToken struct {
	usage  []int
	limits []string
}

func (r *recordingToken) Token(context.Context) (string, error) { return "t", nil }
func (r *recordingToken) ReportUsage(_ string, in, out, cached int) {
	r.usage = append(r.usage, in, out, cached)
}
func (r *recordingToken) ReportLimit(msg string) { r.limits = append(r.limits, msg) }

func sseBody(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "event: x\ndata: %s\n\n", e)
	}
	return b.String()
}

func newTestProvider(t *testing.T, h http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := New(provider.ProfileConfig{Name: "chatgpt", BaseURL: srv.URL, Auth: staticToken("tok-1")})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Provider)
}

func TestNewRequiresAuth(t *testing.T) {
	if _, err := New(provider.ProfileConfig{Name: "x", APIKey: "sk-ignored"}); err == nil {
		t.Fatal("没有登录时应报错")
	}
}

func TestStreamRequestAndEvents(t *testing.T) {
	var got map[string]any
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer tok-1" {
			t.Errorf("请求不对: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		io.WriteString(w, sseBody(
			`{"type":"response.created"}`,
			`{"type":"response.reasoning_summary_part.added","summary_index":0}`,
			`{"type":"response.reasoning_summary_text.delta","delta":"想一想"}`,
			`{"type":"response.reasoning_summary_part.added","summary_index":1}`,
			`{"type":"response.reasoning_summary_text.delta","delta":"再想"}`,
			`not json`,
			`{"type":"response.output_text.delta","delta":"好的"}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.txt\"}"}}`,
			`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":40}}}}`,
		))
	})

	req := provider.Request{
		Model: "gpt-x", System: "你是助手", MaxTokens: 999,
		Messages: []provider.Message{
			provider.UserText("读一下 a.txt"),
			{Role: provider.RoleAssistant, Content: []provider.Block{
				{Type: provider.BlockThinking, Text: "不回传"},
				{Type: provider.BlockText, Text: "我来读"},
				{Type: provider.BlockToolUse, ToolCallID: "call_0", ToolName: "list_dir", ToolInput: json.RawMessage(`{}`)},
			}},
			{Role: provider.RoleUser, Content: []provider.Block{{Type: provider.BlockToolResult, ToolCallID: "call_0", Text: "a.txt"}}},
		},
		Tools: []provider.ToolSpec{{Name: "read_file", Description: "读文件", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	var deltas []provider.Delta
	if err := p.Stream(context.Background(), req, func(d provider.Delta) { deltas = append(deltas, d) }); err != nil {
		t.Fatal(err)
	}

	// 请求体：instructions、store=false、stream=true、没有 max_output_tokens、输入条目顺序正确。
	if got["instructions"] != "你是助手" || got["store"] != false || got["stream"] != true {
		t.Fatalf("请求字段: %v", got)
	}
	if _, ok := got["max_output_tokens"]; ok {
		t.Fatal("套餐额度不接受 max_output_tokens")
	}
	input, _ := json.Marshal(got["input"])
	want := `[{"content":[{"text":"读一下 a.txt","type":"input_text"}],"role":"user","type":"message"},` +
		`{"content":[{"text":"我来读","type":"output_text"}],"role":"assistant","type":"message"},` +
		`{"arguments":"{}","call_id":"call_0","name":"list_dir","type":"function_call"},` +
		`{"call_id":"call_0","output":"a.txt","type":"function_call_output"}]`
	if string(input) != want {
		t.Fatalf("输入条目:\n got  %s\n want %s", input, want)
	}
	tools, _ := json.Marshal(got["tools"])
	if !strings.Contains(string(tools), `"type":"function"`) || !strings.Contains(string(tools), `"name":"read_file"`) {
		t.Fatalf("工具: %s", tools)
	}

	// 事件翻译。
	var thinking, text string
	var call *provider.ToolCall
	var usage *provider.Usage
	var stop provider.StopReason
	for _, d := range deltas {
		switch d.Type {
		case provider.DeltaThinking:
			thinking += d.Text
		case provider.DeltaText:
			text += d.Text
		case provider.DeltaToolCall:
			call = d.ToolCall
		case provider.DeltaUsage:
			usage = d.Usage
		case provider.DeltaStop:
			stop = d.StopReason
		}
	}
	if thinking != "想一想\n\n再想" || text != "好的" {
		t.Fatalf("thinking=%q text=%q", thinking, text)
	}
	if call == nil || call.ID != "call_1" || string(call.Input) != `{"path":"a.txt"}` {
		t.Fatalf("工具调用: %+v", call)
	}
	if usage == nil || usage.InputTokens != 100 || usage.CacheReadTokens != 40 || stop != provider.StopToolUse {
		t.Fatalf("usage=%+v stop=%s", usage, stop)
	}
}

func TestStreamErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"额度用完（流中）", 200, sseBody(`{"type":"response.output_text.delta","delta":"半句"}`, `{"type":"error","code":"subscription_sharing_usage_limit_exceeded","message":"limit"}`), "额度已用完"},
		{"失败事件", 200, sseBody(`{"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`), "boom"},
		{"不支持的功能", 400, `{"error":{"code":"subscription_sharing_unsupported_capability","message":"no","param":"input"}}`, "不支持"},
		{"未授权", 401, `nope`, "重新登录"},
		{"流被截断", 200, sseBody(`{"type":"response.output_text.delta","delta":"半"}`), "unexpectedly"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				io.WriteString(w, c.body)
			})
			err := p.Stream(context.Background(), provider.Request{Model: "m", Messages: []provider.Message{provider.UserText("hi")}}, func(provider.Delta) {})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

func TestReasoningOmitAndMaxTokens(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		io.WriteString(w, sseBody(`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`))
	}))
	defer srv.Close()
	p, _ := New(provider.ProfileConfig{BaseURL: srv.URL, Auth: staticToken("t"), Options: map[string]any{"thinking": "omit"}})
	var stop provider.StopReason
	_ = p.Stream(context.Background(), provider.Request{Model: "m"}, func(d provider.Delta) {
		if d.Type == provider.DeltaStop {
			stop = d.StopReason
		}
	})
	if _, ok := got["reasoning"]; ok {
		t.Fatal("thinking=omit 时不应发送 reasoning")
	}
	if stop != provider.StopMaxTokens {
		t.Fatal(stop)
	}
}

func TestListModels(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"models":[{"slug":"gpt-b"},"gpt-a",{"id":"gpt-c"}]}`)
	})
	got, err := p.ListModels(context.Background())
	if err != nil || strings.Join(got, ",") != "gpt-a,gpt-b,gpt-c" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestUsageReporting(t *testing.T) {
	body := sseBody(`{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	defer srv.Close()
	rec := &recordingToken{}
	p, _ := New(provider.ProfileConfig{BaseURL: srv.URL, Auth: rec})
	if err := p.Stream(context.Background(), provider.Request{Model: "m"}, func(provider.Delta) {}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(rec.usage) != "[7 3 2]" {
		t.Fatalf("应上报用量: %v", rec.usage)
	}
	body = sseBody(`{"type":"error","code":"subscription_sharing_usage_limit_exceeded","message":"limit"}`)
	_ = p.Stream(context.Background(), provider.Request{Model: "m"}, func(provider.Delta) {})
	if len(rec.limits) != 1 {
		t.Fatalf("应上报额度用完: %v", rec.limits)
	}
}
