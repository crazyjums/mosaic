package chatgpt

import (
	"bytes"
	"context"
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
	provider.Register("chatgpt", New)
}

// DefaultBaseURL 是 Responses API 的地址。套餐额度走的是公开 API，不是 chatgpt.com 的内部接口。
const DefaultBaseURL = "https://api.openai.com/v1"

// Provider 是 ChatGPT 套餐额度的适配器。
type Provider struct {
	name     string
	baseURL  string
	auth     provider.TokenSource
	thinking string // adaptive（默认，请求思考摘要）/ omit（不发 reasoning 参数）
	effort   string
	client   *http.Client
}

// New 根据配置档创建适配器。cfg.Auth 必须由登录模块提供（见 internal/settings 如何注入）。
//
// options：
//   - thinking: "adaptive"（默认，界面显示思考摘要）或 "omit"（不发送 reasoning 参数）
//   - effort:   low / medium / high 等，留空使用模型默认
func New(cfg provider.ProfileConfig) (provider.Provider, error) {
	if cfg.Auth == nil {
		return nil, errors.New("尚未登录 ChatGPT 账号（设置 → 模型厂商 → ChatGPT 账号）")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	thinking, _ := cfg.Options["thinking"].(string)
	effort, _ := cfg.Options["effort"].(string)
	client, err := provider.NewHTTPClient(cfg) // 代理和超时
	if err != nil {
		return nil, err
	}
	return &Provider{name: cfg.Name, baseURL: base, auth: cfg.Auth, thinking: thinking, effort: effort, client: client}, nil
}

func (p *Provider) Name() string { return p.name }

// event 是 SSE 每个 data 的结构（只解析用到的字段）。Responses API 的事件名同时出现在 type 字段里。
type event struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	// response.output_item.done
	Item *struct {
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	// response.completed / response.incomplete / response.failed
	Response *struct {
		Status            string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *apiError `json:"error"`
		Usage *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
	// 顶层 error 事件
	Code    string `json:"code"`
	Message string `json:"message"`
	// 思考摘要的分段序号：第二段起前面加空行
	SummaryIndex int `json:"summary_index"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
	Param   string `json:"param"`
}

// usageReporter 是令牌来源的可选能力：想知道每次调用用掉了多少套餐额度（状态栏的"ChatGPT 额度"）。
// OpenAI 没有提供查询剩余额度的接口，只能由我们自己记账。在适配器里记账而不是从事件里统计，
// 是因为标题生成、测试连接这些调用也消耗额度，却不产生对话事件。
type usageReporter interface {
	ReportUsage(model string, input, output, cached int)
	ReportLimit(message string)
}

// Stream 发起请求并把 Responses API 的事件流翻译成 Delta。
func (p *Provider) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) error {
	err := p.stream(ctx, req, onDelta)
	if errors.Is(err, ErrUsageLimit) {
		if r, ok := p.auth.(usageReporter); ok {
			r.ReportLimit(err.Error())
		}
	}
	return err
}

func (p *Provider) stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) error {
	// 步骤 1：构造请求体。注意没有 max_output_tokens：套餐额度这条路不接受它（req.MaxTokens 因此被忽略）。
	body := request{
		Model:        req.Model,
		Instructions: req.System,
		Input:        convertInput(req.Messages),
		Tools:        convertTools(req.Tools),
		Store:        false,
		Stream:       true,
	}
	if p.thinking != "omit" {
		// detailed 而不是 auto：auto 下 GPT-5 系列经常只返回一行加粗的小标题
		// （例如 "**Preparing an SVG mockup**"），思考卡片看起来像是空的。
		// 注意 OpenAI 不公开原始思考过程，这里拿到的始终是摘要。
		body.Reasoning = &reasoningParam{Effort: p.effort, Summary: "detailed"}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}

	// 步骤 2：取令牌（必要时由 TokenSource 刷新），发送请求。
	token, err := p.auth.Token(ctx)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/responses", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpError(resp)
	}

	// 步骤 3：逐个事件翻译。工具调用在 output_item.done 时参数已经完整，不需要自己拼接增量。
	sawToolCall := false
	r := sse.NewReader(resp.Body)
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			// 流结束却没有 response.completed：通常是连接被中断。
			return fmt.Errorf("chatgpt: stream ended unexpectedly: %w", provider.ErrStreamInterrupted)
		}
		if err != nil {
			return err
		}
		var e event
		if json.Unmarshal([]byte(ev.Data), &e) != nil {
			continue // 文档提醒：上游的 SSE 可能夹杂非 JSON 的内容，跳过即可
		}
		switch e.Type {
		case "response.output_text.delta", "response.refusal.delta":
			onDelta(provider.Delta{Type: provider.DeltaText, Text: e.Delta})
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			onDelta(provider.Delta{Type: provider.DeltaThinking, Text: e.Delta})
		case "response.reasoning_summary_part.added":
			if e.SummaryIndex > 0 {
				onDelta(provider.Delta{Type: provider.DeltaThinking, Text: "\n\n"})
			}
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "function_call" {
				sawToolCall = true
				onDelta(provider.Delta{Type: provider.DeltaToolCall, ToolCall: &provider.ToolCall{
					ID: e.Item.CallID, Name: e.Item.Name, Input: normalizeArgs(e.Item.Arguments),
				}})
			}
		case "response.completed", "response.incomplete":
			if e.Response != nil && e.Response.Usage != nil {
				u := e.Response.Usage
				usage := provider.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
				if u.InputTokensDetails != nil {
					usage.CacheReadTokens = u.InputTokensDetails.CachedTokens
				}
				onDelta(provider.Delta{Type: provider.DeltaUsage, Usage: &usage})
				if r, ok := p.auth.(usageReporter); ok {
					r.ReportUsage(req.Model, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens)
				}
			}
			stop := provider.StopEndTurn
			switch {
			case e.Type == "response.incomplete" && e.Response != nil && e.Response.IncompleteDetails != nil &&
				e.Response.IncompleteDetails.Reason == "max_output_tokens":
				stop = provider.StopMaxTokens
			case e.Type == "response.incomplete":
				stop = provider.StopOther
			case sawToolCall:
				stop = provider.StopToolUse
			}
			onDelta(provider.Delta{Type: provider.DeltaStop, StopReason: stop})
			return nil
		case "response.failed":
			if e.Response != nil && e.Response.Error != nil {
				return explain(*e.Response.Error)
			}
			return errors.New("chatgpt: response failed")
		case "error":
			// 额度用完这类错误可能在开始输出之后才到达（文档明确提到），所以流中也要处理。
			return explain(apiError{Code: e.Code, Message: e.Message})
		}
	}
}

// normalizeArgs 保证工具参数是合法的 JSON 对象；模型偶尔输出空字符串。
func normalizeArgs(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" || !json.Valid([]byte(s)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(s)
}

// ErrUsageLimit 表示 ChatGPT 套餐额度已用完（或达到了用户为本应用设置的上限）。
var ErrUsageLimit = provider.NewQuotaError("ChatGPT 套餐额度已用完（或达到了你为本应用设置的上限），请稍后再试或改用 API Key")

// explain 把套餐额度特有的错误码翻译成用户能理解的说明。原始信息附在后面，方便排查。
func explain(e apiError) error {
	if e.Code == "subscription_sharing_usage_limit_exceeded" {
		if msg := strings.TrimSpace(e.Message); msg != "" {
			return fmt.Errorf("%w（%s: %s）", ErrUsageLimit, e.Code, msg)
		}
		return ErrUsageLimit
	}
	hint := map[string]string{
		"subscription_sharing_user_not_eligible":      "这个 ChatGPT 账号不能把套餐额度用于第三方应用（请检查套餐类型或工作区设置）",
		"subscription_sharing_unsupported_capability": "套餐额度不支持这次请求用到的某项功能",
		"subscription_sharing_route_not_supported":    "套餐额度不支持这个接口",
		"invalid_api_key":                             "ChatGPT 登录已失效，请在设置中重新登录",
	}[e.Code]
	msg := strings.TrimSpace(e.Message)
	switch {
	case hint != "" && msg != "":
		return fmt.Errorf("%s（%s: %s）", hint, e.Code, msg)
	case hint != "":
		return errors.New(hint)
	case e.Code != "":
		return fmt.Errorf("chatgpt: %s: %s", e.Code, msg)
	default:
		return fmt.Errorf("chatgpt: %s", msg)
	}
}

func httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(b, &body) == nil && body.Error != nil {
		if resp.StatusCode == http.StatusUnauthorized && body.Error.Code == "" {
			body.Error.Code = "invalid_api_key"
		}
		return provider.NewHTTPError(resp, fmt.Errorf("HTTP %d: %w", resp.StatusCode, explain(*body.Error)))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return provider.NewHTTPError(resp, fmt.Errorf("HTTP 401: %w", explain(apiError{Code: "invalid_api_key"})))
	}
	msg := fmt.Sprintf("chatgpt: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	if resp.StatusCode == http.StatusBadRequest {
		// 接口只回一句 Bad Request，不说是哪里不对。带着插件 / MCP 工具时最常见的原因是某个工具的定义被拒绝了，
		// 给用户一个马上能做的排查办法。
		msg += "（请求被 ChatGPT 拒绝，但没有说明原因。如果刚装了插件或 MCP，可以先在 设置 → 插件 里停用它们再试，确认是不是某个工具的定义被拒绝了）"
	}
	return provider.NewHTTPError(resp, errors.New(msg))
}

// ListModels 获取账号可用的模型，实现 provider.ModelLister。
//
// 套餐额度这条路的 /models 返回 {"models": [...]}，而不是普通 API 的 {"data": [...]}；
// 元素的具体形状文档里没写死，所以两种外层、字符串或对象（id / slug）都接受。
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	token, err := p.auth.Token(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpError(resp)
	}
	var body struct {
		Models []json.RawMessage `json:"models"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("chatgpt: decode model list: %w", err)
	}
	var out []string
	for _, raw := range append(body.Models, body.Data...) {
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			out = append(out, s)
			continue
		}
		var o struct{ ID, Slug, Name string }
		if json.Unmarshal(raw, &o) == nil {
			for _, v := range []string{o.ID, o.Slug, o.Name} {
				if v != "" {
					out = append(out, v)
					break
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
