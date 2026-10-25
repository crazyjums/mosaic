package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// Compactor 是上下文压缩的扩展点，生产环境用 SummaryCompactor，测试/不支持的场景用 NoopCompactor。
//
// 调用时机：agent 循环每一步调用模型之前（见 Agent.prepareContext）。
// 返回 Changed=true 时，内核会用新的消息列表替换会话历史（epoch 加一），并发出 context_compacted 事件。
//
// 为什么放在"每一步之前"而不是"每一轮之前"：一轮里工具结果可能很大（读了好几个文件），
// 上下文在轮中就可能超限，必须在每次请求前都有机会压缩。
//
// 失败语义（Agent 侧实现，见 prepareContext）：
//   - 用户取消：返回 ctx 的错误，本轮按"已取消"结束，历史不变；
//   - 摘要失败（网络、厂商报错、摘要为空）：历史不变，发一条非致命的 compact_failed 警告，
//     本轮继续（也许模型还装得下）；同一轮内不再重试，避免每一步都白白多花一次请求。
type Compactor interface {
	Compact(ctx context.Context, req CompactRequest) (CompactResult, error)
}

type CompactRequest struct {
	Messages []provider.Message
	Model    string
	// SessionID 用于用量记账。
	SessionID string
	// Overhead 是每次请求都会带上的固定开销（系统提示词 + 工具定义）的估算 token 数。
	Overhead int
}

type CompactResult struct {
	Changed      bool
	Messages     []provider.Message
	BeforeTokens int
	AfterTokens  int
	Summary      string
}

// NoopCompactor 从不压缩。
type NoopCompactor struct{}

func (NoopCompactor) Compact(_ context.Context, req CompactRequest) (CompactResult, error) {
	return CompactResult{Messages: req.Messages}, nil
}

// 压缩的默认参数。
const (
	// DefaultCompactTrigger：估算占用超过窗口的这个比例就压缩。留 20% 给本次回答和估算误差
	// （估算按字符数算，和厂商分词器可能差一两成）。
	DefaultCompactTrigger = 0.8
	// DefaultCompactKeep：压缩后原样保留的最近消息至多占窗口的这个比例。
	DefaultCompactKeep = 0.4
	// DefaultCompactKeepTurns：优先原样保留最近几轮（一轮 = 一次用户发言到回答结束）。
	DefaultCompactKeepTurns = 2

	summaryMaxTokens = 4096
	summaryTimeout   = 2 * time.Minute
)

// SummaryCompactor 用模型把较早的对话压缩成一条摘要，最近的消息原样保留。
//
// Window 为 0 表示不知道这个模型的上下文窗口（没有在厂商配置的 models_meta 里填 context_window）：
// 此时无从判断"是否快满了"，选择完全不压缩，而不是猜一个数——猜小了会无谓地丢细节，猜大了没有意义。
type SummaryCompactor struct {
	Provider provider.Provider
	Model    string
	Window   int

	Trigger   float64 // 为 0 用 DefaultCompactTrigger
	Keep      float64 // 为 0 用 DefaultCompactKeep
	KeepTurns int     // 为 0 用 DefaultCompactKeepTurns
}

func (c *SummaryCompactor) Compact(ctx context.Context, req CompactRequest) (CompactResult, error) {
	msgs := req.Messages
	unchanged := CompactResult{Messages: msgs}
	if c.Window <= 0 || c.Provider == nil {
		return unchanged, nil
	}
	trigger := orFloat(c.Trigger, DefaultCompactTrigger)
	keep := orFloat(c.Keep, DefaultCompactKeep)
	keepTurns := c.KeepTurns
	if keepTurns <= 0 {
		keepTurns = DefaultCompactKeepTurns
	}

	before := req.Overhead + estimateMessages(msgs)
	if float64(before) <= float64(c.Window)*trigger {
		return unchanged, nil
	}
	cut := chooseCut(msgs, int(float64(c.Window)*keep), keepTurns)
	// 没有可切的位置，或者被切掉的只是上一次的摘要（没有新内容可摘）：压不动。
	if cut <= 0 || (cut == 1 && msgs[0].IsSummary()) {
		return unchanged, nil
	}

	summary, err := c.summarize(ctx, req, msgs[:cut])
	if err != nil {
		return unchanged, err
	}
	out := make([]provider.Message, 0, len(msgs)-cut+1)
	out = append(out, summaryMessage(summary))
	out = append(out, msgs[cut:]...)
	after := req.Overhead + estimateMessages(out)
	// 摘要比原文还大（原文本来就很短、摘要写得很啰嗦）：不换，免得越压越大。
	if after >= before {
		return unchanged, nil
	}
	return CompactResult{Changed: true, Messages: out, BeforeTokens: before, AfterTokens: after, Summary: summary}, nil
}

func orFloat(v, def float64) float64 {
	if v <= 0 {
		return def
	}
	return v
}

func summaryMessage(summary string) provider.Message {
	return provider.Message{Role: provider.RoleUser, Content: []provider.Block{{Type: provider.BlockText, Text: provider.SummaryHeader + summary}}}
}

// chooseCut 返回要摘要的消息数 n（msgs[:n] 被摘要，msgs[n:] 原样保留），-1 表示没有安全的切点。
//
// 安全切点：切点之后的第一条消息必须是"用户发言"，或"紧跟在 user 消息之后的 assistant 消息"。
// tool_use 总在 assistant 消息里、对应的 tool_result 在紧随其后的 user 消息里，
// 所以这两种位置都不会把一对 tool_use / tool_result 拆开，也不会让保留部分以孤立的 tool_result 开头。
//
// 选择：优先保留最近 keepTurns 轮；如果这些就超过保留预算（一轮里读了很多大文件），
// 就往后挑第一个能装进预算的切点——代价是当前轮的前半段也被摘要；
// 连最后一个单元都装不下时，保留最后一个切点之后的内容。
func chooseCut(msgs []provider.Message, budget, keepTurns int) int {
	var cands []int
	var turns []int
	for i := 1; i < len(msgs); i++ {
		m := msgs[i]
		isInput := m.IsUserInput()
		if isInput {
			turns = append(turns, i)
		}
		if isInput || (m.Role == provider.RoleAssistant && msgs[i-1].Role == provider.RoleUser) {
			cands = append(cands, i)
		}
	}
	if len(cands) == 0 {
		return -1
	}
	suffix := make([]int, len(msgs)+1)
	for i := len(msgs) - 1; i >= 0; i-- {
		suffix[i] = suffix[i+1] + estimateMessage(msgs[i])
	}
	min := 1
	if len(turns) >= keepTurns {
		min = turns[len(turns)-keepTurns]
	}
	for _, c := range cands {
		if c >= min && suffix[c] <= budget {
			return c
		}
	}
	return cands[len(cands)-1]
}

func estimateMessages(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		n += estimateMessage(m)
	}
	return n
}

func estimateMessage(m provider.Message) int {
	n := 4 // 每条消息的固定结构开销
	for _, b := range m.Content {
		switch b.Type {
		case provider.BlockImage:
			n += imageTokenEstimate
		case provider.BlockToolUse:
			n += EstimateTokens(b.ToolName) + EstimateTokens(string(b.ToolInput))
		default:
			n += EstimateTokens(b.Text)
		}
	}
	return n
}

const summaryInstruction = "You are compacting the earlier part of a conversation between a user and a coding/work assistant, " +
	"so the conversation can continue with less context. Write a summary that lets the assistant carry on seamlessly. " +
	"Write in the same language the user used. Be concrete and compact. Include, as short sections:\n" +
	"1. The user's goal and any instructions, constraints or preferences that still apply.\n" +
	"2. What has been done so far: decisions made, files created or changed (with paths), commands run and their outcomes.\n" +
	"3. Important facts discovered (names, paths, values, errors) that later steps may need.\n" +
	"4. What is still open: unfinished work and the next step.\n" +
	"If the text contains an earlier summary, merge it into yours instead of repeating it. " +
	"Do not invent anything that is not in the conversation. Output only the summary."

// summarize 让模型为 head 生成摘要。
func (c *SummaryCompactor) summarize(ctx context.Context, req CompactRequest, head []provider.Message) (string, error) {
	// 转写稿本身也要装进窗口：逐步收紧单项长度，直到估算不超过窗口的 60%。
	var transcript string
	for cap := 4000; ; cap /= 2 {
		transcript = renderTranscript(head, cap)
		if float64(EstimateTokens(transcript)) <= float64(c.Window)*0.6 || cap <= 100 {
			break
		}
	}
	if float64(EstimateTokens(transcript)) > float64(c.Window)*0.6 {
		// 收到最紧也装不下（消息条数极多）：保留靠后的部分，较早的直接丢弃，摘要里注明。
		r := []rune(transcript)
		keep := int(float64(c.Window) * 0.6)
		if keep < len(r) {
			transcript = "[…earlier part omitted…]\n" + string(r[len(r)-keep:])
		}
	}

	ctx = provider.WithCall(ctx, provider.CallInfo{SessionID: req.SessionID, Source: provider.SourceCompact})
	ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	var b strings.Builder
	err := c.Provider.Stream(ctx, provider.Request{
		Model:     c.Model,
		System:    summaryInstruction,
		Messages:  []provider.Message{provider.UserText("<conversation>\n" + transcript + "\n</conversation>")},
		MaxTokens: summaryMaxTokens,
	}, func(d provider.Delta) {
		if d.Type == provider.DeltaText {
			b.WriteString(d.Text)
		}
	})
	if err != nil {
		return "", fmt.Errorf("生成摘要失败: %w", err)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", errors.New("生成摘要失败: 模型没有返回内容")
	}
	return out, nil
}

// renderTranscript 把消息转写成纯文本，每个文本块/工具输入/工具结果最多保留 cap 个字符。
// 思考内容不进摘要（它是模型的内部草稿，且常常很长）；图片只留占位。
func renderTranscript(msgs []provider.Message, cap int) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Content {
			switch blk.Type {
			case provider.BlockText:
				who := "User"
				if m.Role == provider.RoleAssistant {
					who = "Assistant"
				}
				fmt.Fprintf(&b, "%s: %s\n", who, clip(blk.Text, cap))
			case provider.BlockToolUse:
				in := blk.ToolInput
				fmt.Fprintf(&b, "Assistant called tool %s: %s\n", blk.ToolName, clip(string(in), cap/4+1))
			case provider.BlockToolResult:
				tag := "Tool result"
				if blk.IsError {
					tag = "Tool error"
				}
				fmt.Fprintf(&b, "%s: %s\n", tag, clip(blk.Text, cap))
			case provider.BlockImage:
				b.WriteString("User attached an image.\n")
			}
		}
	}
	return b.String()
}
