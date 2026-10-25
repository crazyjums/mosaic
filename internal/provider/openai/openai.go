package openai

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/provider/sse"
)

func init() {
	provider.Register("openai", New)
}

const defaultBaseURL = "https://api.openai.com/v1"

// Provider 是 OpenAI 兼容协议的适配器。
type Provider struct {
	name           string
	baseURL        string
	apiKey         string
	maxTokens      int
	maxTokensField string
	client         *http.Client
}

// New 根据配置档创建适配器。
//
// options：
//   - max_tokens_field: "max_tokens"（默认，兼容面最广）或 "max_completion_tokens"（OpenAI o 系列/新模型）
//   - allow_no_key:     true 时允许不配置密钥（本地 Ollama / vLLM）
func New(cfg provider.ProfileConfig) (provider.Provider, error) {
	allowNoKey, _ := cfg.Options["allow_no_key"].(bool)
	if cfg.APIKey == "" && !allowNoKey {
		return nil, errors.New("missing API key (set api_key_env or api_key; local servers can set options.allow_no_key = true)")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}
	field, _ := cfg.Options["max_tokens_field"].(string)
	// 代理和超时见 provider.NewHTTPClient；不设整体超时：流式响应可能持续好几分钟，超时与取消统一交给 ctx。
	client, err := provider.NewHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Provider{
		name:           cfg.Name,
		baseURL:        base,
		apiKey:         cfg.APIKey,
		maxTokens:      cfg.MaxTokens,
		maxTokensField: field,
		client:         client,
	}, nil
}

func (p *Provider) Name() string { return p.name }

// streamChunk 是 SSE 每个 data 的结构（只解析用到的字段）。
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
			// 非标准但常见的思考字段：DeepSeek / Qwen 用 reasoning_content，OpenRouter 等用 reasoning。
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

// pendingCall 是正在拼接中的工具调用。OpenAI 把一个工具调用拆成多个 chunk：
// 第一个带 id 和 name，后续只带 arguments 的片段，用 index 关联。
type pendingCall struct {
	id, name string
	args     strings.Builder
}

// Stream 发起请求并把 SSE 流翻译成 Delta。
func (p *Provider) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) error {
	// 步骤 1：构造请求体。
	body := chatRequest{
		Model:         req.Model,
		Messages:      convertMessages(req.System, req.Messages),
		Tools:         convertTools(req.Tools),
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true}, // 否则流式响应不返回用量
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.maxTokens
	}
	if p.maxTokensField == "max_completion_tokens" {
		body.MaxCompletionTokens = maxTokens
	} else {
		body.MaxTokens = maxTokens
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}

	// 步骤 2：发送 HTTP 请求。
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpError(resp)
	}

	// 步骤 3：逐个读取 SSE 事件并翻译。
	calls := map[int]*pendingCall{}
	var usage *provider.Usage
	finish := ""
	// ended 表示收到过正常结束的信号（[DONE] 或 finish_reason）。有些兼容服务不发 [DONE]，
	// 所以两者有其一即可；都没有就断流了，说明连接被意外中断，不能把残缺的输出当作完整回复。
	ended := false
	r := sse.NewReader(resp.Body)
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			if !ended {
				return fmt.Errorf("openai: stream ended before finish_reason / [DONE]: %w", provider.ErrStreamInterrupted)
			}
			break
		}
		if err != nil {
			return err
		}
		if ev.Data == "[DONE]" {
			ended = true
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			return fmt.Errorf("openai: decode chunk: %w", err)
		}
		if chunk.Error != nil {
			return fmt.Errorf("openai: stream error: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			u := provider.Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
			if d := chunk.Usage.PromptTokensDetails; d != nil {
				u.CacheReadTokens = d.CachedTokens
			}
			usage = &u
		}
		for _, ch := range chunk.Choices {
			d := ch.Delta
			if t := d.ReasoningContent + d.Reasoning; t != "" {
				onDelta(provider.Delta{Type: provider.DeltaThinking, Text: t})
			}
			if d.Content != "" {
				onDelta(provider.Delta{Type: provider.DeltaText, Text: d.Content})
			}
			for _, tc := range d.ToolCalls {
				pc := calls[tc.Index]
				if pc == nil {
					pc = &pendingCall{}
					calls[tc.Index] = pc
				}
				if tc.ID != "" {
					pc.id = tc.ID
				}
				if tc.Function.Name != "" {
					pc.name = tc.Function.Name
				}
				pc.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != "" {
				finish = ch.FinishReason
				ended = true
			}
		}
	}

	// 步骤 4：流结束，按 index 顺序发出拼好的工具调用，再发用量和停止原因。
	// 工具调用放在最后统一发出，是因为只有流结束时才能确定参数已经完整。
	indexes := make([]int, 0, len(calls))
	for i := range calls {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		pc := calls[i]
		// 部分兼容服务（一些本地服务和兼容层）不返回调用 id。空 id 会让界面上的工具卡片互相覆盖，
		// 下一次请求的 tool_call_id 也对不上，所以在这里补一个。
		if pc.id == "" {
			pc.id = fmt.Sprintf("call_%d_%s", i, randHex())
		}
		onDelta(provider.Delta{Type: provider.DeltaToolCall, ToolCall: &provider.ToolCall{
			ID: pc.id, Name: pc.name, Input: normalizeArgs(pc.args.String()),
		}})
	}
	if usage != nil {
		onDelta(provider.Delta{Type: provider.DeltaUsage, Usage: usage})
	}
	stop := mapFinishReason(finish)
	if len(calls) > 0 {
		stop = provider.StopToolUse
	}
	onDelta(provider.Delta{Type: provider.DeltaStop, StopReason: stop})
	return nil
}

func randHex() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// normalizeArgs 保证工具参数是合法 JSON 对象。
// 模型偶尔会输出不合法的参数；原样传下去会导致事件 JSON 序列化失败，
// 所以包装成 {"_raw": "..."}，工具解析时会报参数错误，模型看到后通常会自我纠正。
func normalizeArgs(s string) json.RawMessage {
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

func mapFinishReason(r string) provider.StopReason {
	switch r {
	case "stop", "":
		return provider.StopEndTurn
	case "tool_calls", "function_call":
		return provider.StopToolUse
	case "length":
		return provider.StopMaxTokens
	case "content_filter":
		return provider.StopRefusal
	}
	return provider.StopOther
}

// httpError 把非 200 响应转换成可读的错误，尽量提取厂商返回的 message。
func httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != nil && e.Error.Message != "" {
		return provider.NewHTTPError(resp, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, e.Error.Message))
	}
	return provider.NewHTTPError(resp, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b))))
}
