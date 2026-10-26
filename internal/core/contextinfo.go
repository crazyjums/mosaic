package core

import (
	"encoding/json"
	"sort"
	"unicode/utf8"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// 上下文明细（界面右侧的"上下文"面板）：下一次请求会发给模型哪些内容，各占多少。
//
// token 数是按字符估算的：各厂商的分词器不同，又不能为了一个展示功能引入几个分词库。
// 估算规则（经验值）：ASCII 字符约 4 个一个 token，其他字符（中文等）约 1 个一个 token，
// 图片按固定值计。准确的总数以厂商返回的用量为准（界面上同时显示最近一次请求的真实值）。

// imageTokenEstimate 是一张图片的估算 token 数（不同厂商按分辨率计费，差别很大，这里只取一个量级）。
const imageTokenEstimate = 1000

// EstimateTokens 估算一段文本的 token 数。
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	ascii, other := 0, 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r < utf8.RuneSelf {
			ascii++
		} else {
			other++
		}
		i += size
	}
	return (ascii+3)/4 + other
}

// ContextPart 是一类内容的合计。
type ContextPart struct {
	// Kind：system / tools / user / assistant / thinking / tool_call / tool_result / image
	Kind   string `json:"kind"`
	Count  int    `json:"count"`
	Tokens int    `json:"tokens"`
}

// ContextItem 是单条内容，用于列出"占用最多的几项"。
// 不在这里拼好显示文字（"第 3 条用户消息"），由界面按语言格式化。
type ContextItem struct {
	Kind string `json:"kind"`
	// Index：user 是第几条用户消息，assistant / thinking 是第几次回答（从 1 开始）。
	Index int `json:"index,omitempty"`
	// Tool：tool_call / tool_result 的工具名。
	Tool   string `json:"tool,omitempty"`
	Detail string `json:"detail"` // 内容开头的一小段，便于认出是哪一条
	Tokens int    `json:"tokens"`
}

// ContextBreakdown 是上下文明细。
type ContextBreakdown struct {
	Parts    []ContextPart `json:"parts"`
	Largest  []ContextItem `json:"largest"`
	Total    int           `json:"total"`
	Messages int           `json:"messages"`
	// ToolCount 是发给模型的工具数（不含被禁用的）。
	ToolCount int `json:"tool_count"`
}

// maxLargest 是"占用最多的几项"列出的条数。
const maxLargest = 8

// ContextBreakdown 估算当前会话下一次请求的上下文组成：系统提示词、工具定义、历史消息。
func (e *Engine) ContextBreakdown() ContextBreakdown {
	e.mu.Lock()
	sess := e.session
	rt := e.runtimeLocked(sess)
	disabled := e.disabledLocked()
	e.mu.Unlock()

	parts := map[string]*ContextPart{}
	order := []string{"system", "tools", "user", "assistant", "thinking", "tool_call", "tool_result", "image"}
	for _, k := range order {
		parts[k] = &ContextPart{Kind: k}
	}
	var items []ContextItem
	add := func(kind string, tokens int, item *ContextItem) {
		parts[kind].Count++
		parts[kind].Tokens += tokens
		if item != nil && tokens > 0 {
			item.Kind, item.Tokens = kind, tokens
			items = append(items, *item)
		}
	}

	// 系统提示词与工具定义：每次请求都会带上，是固定开销。
	add("system", EstimateTokens(rt.SystemPrompt), &ContextItem{Detail: snippet(rt.SystemPrompt)})
	toolCount := 0
	if e.cfg.Tools != nil {
		for _, s := range e.cfg.Tools.Specs() {
			if disabled[s.Name] {
				continue
			}
			toolCount++
			def, _ := json.Marshal(map[string]any{"name": s.Name, "description": s.Description, "input_schema": s.InputSchema})
			add("tools", EstimateTokens(string(def)), nil)
		}
	}

	// 历史消息。工具调用和结果按工具名标注，便于看出是哪次 read_file 返回了一大段内容。
	msgs := sess.messages()
	toolNames := map[string]string{}
	userN, assistantN := 0, 0
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			if m.IsUserInput() {
				userN++
			}
		case provider.RoleAssistant:
			assistantN++
		}
		for _, b := range m.Content {
			switch b.Type {
			case provider.BlockText:
				if m.Role == provider.RoleUser {
					add("user", EstimateTokens(b.Text), &ContextItem{Index: userN, Detail: snippet(b.Text)})
				} else {
					add("assistant", EstimateTokens(b.Text), &ContextItem{Index: assistantN, Detail: snippet(b.Text)})
				}
			case provider.BlockThinking:
				// 有的厂商（Anthropic）会在之后的请求里去掉旧回合的思考，这里按全部计入，是偏大的估计。
				add("thinking", EstimateTokens(b.Text), &ContextItem{Index: assistantN, Detail: snippet(b.Text)})
			case provider.BlockToolUse:
				toolNames[b.ToolCallID] = b.ToolName
				add("tool_call", EstimateTokens(b.ToolName)+EstimateTokens(string(b.ToolInput)), &ContextItem{Tool: b.ToolName, Detail: snippet(string(b.ToolInput))})
			case provider.BlockToolResult:
				add("tool_result", EstimateTokens(b.Text), &ContextItem{Tool: toolNames[b.ToolCallID], Detail: snippet(b.Text)})
			case provider.BlockImage:
				add("image", imageTokenEstimate, &ContextItem{Detail: b.MediaType})
			}
		}
	}

	out := ContextBreakdown{Messages: len(msgs), ToolCount: toolCount}
	for _, k := range order {
		p := parts[k]
		out.Total += p.Tokens
		if p.Count > 0 {
			out.Parts = append(out.Parts, *p)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Tokens > items[j].Tokens })
	if len(items) > maxLargest {
		items = items[:maxLargest]
	}
	out.Largest = items
	if out.Largest == nil {
		out.Largest = []ContextItem{}
	}
	return out
}

// snippet 取内容开头的一小段，压成一行。
func snippet(s string) string {
	const n = 60
	r := make([]rune, 0, n)
	space := false
	for _, c := range s {
		if c == '\n' || c == '\r' || c == '\t' || c == ' ' {
			if !space && len(r) > 0 {
				r = append(r, ' ')
			}
			space = true
			continue
		}
		space = false
		r = append(r, c)
		if len(r) >= n {
			return string(r) + "…"
		}
	}
	return string(r)
}
