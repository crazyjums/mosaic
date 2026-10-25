package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/provider/sse"
)

func init() {
	provider.Register("anthropic", New)
}

const (
	defaultBaseURL   = "https://api.anthropic.com"
	apiVersion       = "2023-06-01"
	defaultMaxTokens = 64000 // 流式请求不怕 HTTP 超时，给足输出空间
	fallbackBeta     = "server-side-fallback-2026-07-01"
)

// Provider 是 Anthropic 原生协议适配器。
type Provider struct {
	name      string
	baseURL   string
	apiKey    string
	maxTokens int
	thinking  string // "adaptive"（默认）或 "omit"
	effort    string
	fallback  bool
	client    *http.Client
}

// New 根据配置档创建适配器。
//
// options：
//   - thinking:         "adaptive"（默认，带 display=summarized）或 "omit"（不发送 thinking 参数，用于不支持的旧模型/代理）
//   - effort:           low / medium / high / xhigh / max，留空则使用模型默认值
//   - refusal_fallback: true 时开启服务端拒绝回退（fallbacks="default" + beta 头）
func New(cfg provider.ProfileConfig) (provider.Provider, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("missing API key (set api_key_env, e.g. ANTHROPIC_API_KEY)")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}
	p := &Provider{
		name:      cfg.Name,
		baseURL:   base,
		apiKey:    cfg.APIKey,
		maxTokens: cfg.MaxTokens,
		thinking:  "adaptive",
		client:    &http.Client{},
	}
	if v, _ := cfg.Options["thinking"].(string); v != "" {
		p.thinking = v
	}
	p.effort, _ = cfg.Options["effort"].(string)
	p.fallback, _ = cfg.Options["refusal_fallback"].(bool)
	return p, nil
}

func (p *Provider) Name() string { return p.name }

// streamEvent 是各类 SSE data 的并集结构，按 Type 读取对应字段。
type streamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`

	Message *struct {
		Usage apiUsage `json:"usage"`
	} `json:"message"` // message_start

	ContentBlock *struct {
		Type string `json:"type"` // text / thinking / redacted_thinking / tool_use
		ID   string `json:"id"`
		Name string `json:"name"`
		Data string `json:"data"`
	} `json:"content_block"` // content_block_start

	Delta *struct {
		Type        string `json:"type"` // text_delta / thinking_delta / signature_delta / input_json_delta
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"` // message_delta
	} `json:"delta"`

	Usage *apiUsage `json:"usage"` // message_delta

	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type apiUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// openBlock 记录正在接收的内容块。只有 tool_use 需要在本地拼接（参数以 JSON 片段到达），
// 其他块的增量直接转发。
type openBlock struct {
	kind string
	id   string
	name string
	json strings.Builder
}

// Stream 发起请求并把 SSE 事件翻译成 Delta。
func (p *Provider) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) error {
	// 步骤 1：构造请求体。
	body := messagesRequest{
		Model:     req.Model,
		MaxTokens: firstNonZero(req.MaxTokens, p.maxTokens, defaultMaxTokens),
		System:    req.System,
		Messages:  convertMessages(req.Messages),
		Tools:     convertTools(req.Tools),
		Stream:    true,
	}
	if p.thinking == "adaptive" {
		body.Thinking = &thinkingConfig{Type: "adaptive", Display: "summarized"}
	}
	if p.effort != "" {
		body.OutputConfig = &outputConfig{Effort: p.effort}
	}
	if p.fallback {
		body.Fallbacks = "default"
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}

	// 步骤 2：发送请求。
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)
	if p.fallback {
		httpReq.Header.Set("anthropic-beta", fallbackBeta)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpError(resp)
	}

	// 步骤 3：逐个处理 SSE 事件。
	blocks := map[int]*openBlock{}
	var usage provider.Usage
	stop := provider.StopEndTurn
	r := sse.NewReader(resp.Body)
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			// 正常情况下应先收到 message_stop；没收到就断流，说明连接被意外中断。
			return errors.New("anthropic: stream ended before message_stop")
		}
		if err != nil {
			return err
		}
		var se streamEvent
		if err := json.Unmarshal([]byte(ev.Data), &se); err != nil {
			return fmt.Errorf("anthropic: decode event %q: %w", ev.Name, err)
		}

		switch se.Type {
		case "message_start":
			if se.Message != nil {
				u := se.Message.Usage
				usage.InputTokens = u.InputTokens
				usage.CacheReadTokens = u.CacheReadInputTokens
				usage.CacheWriteTokens = u.CacheCreationInputTokens
				usage.OutputTokens = u.OutputTokens
			}

		case "content_block_start":
			if se.ContentBlock == nil {
				continue
			}
			b := &openBlock{kind: se.ContentBlock.Type, id: se.ContentBlock.ID, name: se.ContentBlock.Name}
			blocks[se.Index] = b
			// 加密思考块在 start 事件里就带着完整数据，之后不会有 delta。
			if b.kind == "redacted_thinking" {
				onDelta(provider.Delta{Type: provider.DeltaRedactedThinking, Text: se.ContentBlock.Data})
			}

		case "content_block_delta":
			b, d := blocks[se.Index], se.Delta
			if b == nil || d == nil {
				continue
			}
			switch d.Type {
			case "text_delta":
				onDelta(provider.Delta{Type: provider.DeltaText, Text: d.Text})
			case "thinking_delta":
				onDelta(provider.Delta{Type: provider.DeltaThinking, Text: d.Thinking})
			case "signature_delta":
				onDelta(provider.Delta{Type: provider.DeltaThinkingSignature, Text: d.Signature})
			case "input_json_delta":
				b.json.WriteString(d.PartialJSON)
			}

		case "content_block_stop":
			// 工具调用块结束时参数才完整，此时发出 DeltaToolCall。
			b := blocks[se.Index]
			delete(blocks, se.Index)
			if b != nil && b.kind == "tool_use" {
				onDelta(provider.Delta{Type: provider.DeltaToolCall, ToolCall: &provider.ToolCall{
					ID: b.id, Name: b.name, Input: normalizeInput(b.json.String()),
				}})
			}

		case "message_delta":
			if se.Delta != nil && se.Delta.StopReason != "" {
				stop = mapStopReason(se.Delta.StopReason)
			}
			if se.Usage != nil {
				// message_delta 里的 output_tokens 是累计值。
				usage.OutputTokens = se.Usage.OutputTokens
			}

		case "message_stop":
			u := usage
			onDelta(provider.Delta{Type: provider.DeltaUsage, Usage: &u})
			onDelta(provider.Delta{Type: provider.DeltaStop, StopReason: stop})
			return nil

		case "error":
			if se.Error != nil {
				return fmt.Errorf("anthropic: stream error (%s): %s", se.Error.Type, se.Error.Message)
			}
			return errors.New("anthropic: stream error")

		case "ping":
			// 心跳，忽略。
		}
	}
}

// normalizeInput 保证工具参数是合法的 JSON 对象（与 openai 适配器同理）。
// 没有参数的工具调用不会收到任何 input_json_delta，此时参数为 {}。
func normalizeInput(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(map[string]string{"_raw": s})
	return b
}

func mapStopReason(r string) provider.StopReason {
	switch r {
	case "end_turn", "stop_sequence":
		return provider.StopEndTurn
	case "tool_use":
		return provider.StopToolUse
	case "max_tokens":
		return provider.StopMaxTokens
	case "refusal":
		return provider.StopRefusal
	}
	return provider.StopOther
}

func httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != nil {
		return fmt.Errorf("anthropic: HTTP %d %s: %s", resp.StatusCode, e.Error.Type, e.Error.Message)
	}
	return fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

func firstNonZero(vs ...int) int {
	for _, v := range vs {
		if v != 0 {
			return v
		}
	}
	return 0
}
