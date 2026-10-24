package fake

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
)

func init() {
	provider.Register("fake", New)
}

// Script 是一份剧本：一轮对话中模型依次"说"的每一步。
//
// 第 N 次模型调用（从本轮用户发言算起）返回 Steps[N-1]；超出剧本长度时返回一句结束语。
type Script struct {
	Steps []Step `json:"steps"`
}

// Step 是一次模型调用的输出。字段都是可选的，按 思考 → 文本 → 图片 → 工具调用 的顺序输出。
//
// 用"高层字段"而不是直接写 Delta 序列，是为了让剧本 JSON 好写好读；
// 切分成流式片段由 Provider 自动完成。
type Step struct {
	Thinking  string     `json:"thinking,omitempty"`
	Text      string     `json:"text,omitempty"`
	Image     *ImageSpec `json:"image,omitempty"`
	ToolCalls []ToolSpec `json:"tool_calls,omitempty"`
	// Error 非空时，输出完上面的内容后返回这个错误，用于测试"厂商中途出错"。
	Error string `json:"error,omitempty"`
}

type ToolSpec struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ImageSpec 描述一张图片。Builtin 为 "demo" 时使用内置生成的演示图。
type ImageSpec struct {
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	Builtin   string `json:"builtin,omitempty"`
}

// Provider 是假厂商。
type Provider struct {
	name   string
	script Script
	// delay 是每个流式片段之间的间隔。测试里为 0，演示时设成几十毫秒才能看出"流式"效果。
	delay time.Duration
}

// New 是注册到 provider.Registry 的工厂。
//
// 支持的 options：
//   - scenario: "demo"（默认）或 "echo"
//   - script:   JSON 剧本文件路径（优先于 scenario）
//   - delay_ms: 每个片段的延迟毫秒数
func New(cfg provider.ProfileConfig) (provider.Provider, error) {
	p := &Provider{name: cfg.Name}
	if v, ok := cfg.Options["delay_ms"]; ok {
		ms, err := toInt(v)
		if err != nil {
			return nil, fmt.Errorf("fake: delay_ms: %w", err)
		}
		p.delay = time.Duration(ms) * time.Millisecond
	}

	if path, _ := cfg.Options["script"].(string); path != "" {
		s, err := LoadScript(path)
		if err != nil {
			return nil, err
		}
		p.script = s
		return p, nil
	}
	switch scenario, _ := cfg.Options["scenario"].(string); scenario {
	case "", "demo":
		p.script = DemoScript()
	case "echo":
		p.script = Script{} // 空剧本：直接走结束语逻辑，即复述用户输入
	default:
		return nil, fmt.Errorf("fake: unknown scenario %q", scenario)
	}
	return p, nil
}

// NewWithScript 直接用剧本创建 Provider，供测试使用。
func NewWithScript(name string, s Script, delay time.Duration) *Provider {
	return &Provider{name: name, script: s, delay: delay}
}

// LoadScript 从 JSON 文件读取剧本。
func LoadScript(path string) (Script, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Script{}, fmt.Errorf("fake: read script: %w", err)
	}
	var s Script
	if err := json.Unmarshal(b, &s); err != nil {
		return Script{}, fmt.Errorf("fake: parse script %s: %w", path, err)
	}
	return s, nil
}

func (p *Provider) Name() string { return p.name }

// Stream 按剧本输出当前这一步。
func (p *Provider) Stream(ctx context.Context, req provider.Request, onDelta func(provider.Delta)) error {
	// 步骤 1：确定进行到剧本的第几步——数一数最后一条用户发言之后有几条 assistant 消息。
	userText, idx := progress(req.Messages)

	var step Step
	if idx < len(p.script.Steps) {
		step = p.script.Steps[idx]
	} else {
		step = Step{Text: fmt.Sprintf("（fake）你说的是：%s", userText)}
		if len(p.script.Steps) > 0 {
			step = Step{Text: "（fake）剧本已经演完了。"}
		}
	}

	// 步骤 2：按顺序流式输出各部分。{{input}} 占位符替换为用户输入，让演示看起来更"真实"。
	expand := func(s string) string { return strings.ReplaceAll(s, "{{input}}", userText) }
	out := 0
	emitChunks := func(t provider.DeltaType, s string) error {
		for _, c := range chunks(expand(s), 6) {
			if err := p.sleep(ctx); err != nil {
				return err
			}
			out += len(c)
			onDelta(provider.Delta{Type: t, Text: c})
		}
		return nil
	}

	if err := emitChunks(provider.DeltaThinking, step.Thinking); err != nil {
		return err
	}
	if err := emitChunks(provider.DeltaText, step.Text); err != nil {
		return err
	}
	if step.Image != nil {
		img := provider.Image{MediaType: step.Image.MediaType, Data: step.Image.Data}
		if step.Image.Builtin == "demo" {
			img = provider.Image{MediaType: "image/png", Data: demoImage}
		}
		onDelta(provider.Delta{Type: provider.DeltaImage, Image: &img})
	}
	for _, tc := range step.ToolCalls {
		if err := p.sleep(ctx); err != nil {
			return err
		}
		// 在 JSON 里替换占位符时要先做 JSON 转义，否则用户输入里的引号会破坏 JSON。
		quoted, _ := json.Marshal(userText)
		input := json.RawMessage(strings.ReplaceAll(string(tc.Input), "{{input}}", string(quoted[1:len(quoted)-1])))
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		onDelta(provider.Delta{Type: provider.DeltaToolCall, ToolCall: &provider.ToolCall{
			ID: randomID("call"), Name: tc.Name, Input: input,
		}})
	}

	// 步骤 3：用量（粗略按 4 字节一个 token 估算）和停止原因。
	onDelta(provider.Delta{Type: provider.DeltaUsage, Usage: &provider.Usage{
		InputTokens:  estimateTokens(req),
		OutputTokens: out/4 + 1,
	}})
	if step.Error != "" {
		return fmt.Errorf("fake: %s", step.Error)
	}
	stop := provider.StopEndTurn
	if len(step.ToolCalls) > 0 {
		stop = provider.StopToolUse
	}
	onDelta(provider.Delta{Type: provider.DeltaStop, StopReason: stop})
	return nil
}

func (p *Provider) sleep(ctx context.Context) error {
	if p.delay <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(p.delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// progress 返回最后一条用户发言的文本，以及它之后已经有多少条 assistant 消息。
func progress(msgs []provider.Message) (string, int) {
	n := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == provider.RoleAssistant {
			n++
			continue
		}
		if t := m.Text(); t != "" {
			return t, n
		}
	}
	return "", n
}

// chunks 把字符串按 rune 切成小段，模拟流式输出。
func chunks(s string, size int) []string {
	if s == "" {
		return nil
	}
	r := []rune(s)
	var out []string
	for i := 0; i < len(r); i += size {
		j := min(i+size, len(r))
		out = append(out, string(r[i:j]))
	}
	return out
}

func estimateTokens(req provider.Request) int {
	n := len(req.System)
	for _, m := range req.Messages {
		for _, b := range m.Content {
			n += len(b.Text) + len(b.ToolInput)
		}
	}
	return n/4 + 1
}

func randomID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func toInt(v any) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case float64:
		return int(x), nil
	}
	return 0, fmt.Errorf("expected number, got %T", v)
}
