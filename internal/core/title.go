package core

import (
	"context"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// isFirstTurn 判断会话是否刚完成第一轮（只有一条用户发言）。
func isFirstTurn(s *Session) bool {
	n := 0
	for _, m := range s.messages() {
		if m.Role == provider.RoleUser && m.Text() != "" {
			n++
		}
	}
	return n == 1
}

// generateTitle 让模型为会话生成一个简短标题（设置 → 外观与对话 → 会话标题："由模型生成"）。
//
// 只发送第一轮的用户输入和最后一段回答，而不是整个历史：标题只需要知道"聊的是什么"，
// 这样也让这次额外调用的花费保持在几百个 token 以内。用同一个厂商和模型，不需要额外配置。
func generateTitle(ctx context.Context, p provider.Provider, model string, s *Session) (string, error) {
	var user, reply string
	for _, m := range s.messages() {
		if t := m.Text(); t != "" {
			if m.Role == provider.RoleUser && user == "" {
				user = t
			} else if m.Role == provider.RoleAssistant {
				reply = t
			}
		}
	}
	prompt := "Write a short title for the conversation below, in the same language as the user's message. " +
		"At most 20 Chinese characters or 8 English words. Output only the title, without quotes or punctuation at the end.\n\n" +
		"User: " + clip(user, 2000) + "\n\nAssistant: " + clip(reply, 2000)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var b strings.Builder
	err := p.Stream(ctx, provider.Request{
		Model:     model,
		Messages:  []provider.Message{provider.UserText(prompt)},
		MaxTokens: 1024, // 留给可能的思考；标题本身很短
	}, func(d provider.Delta) {
		if d.Type == provider.DeltaText {
			b.WriteString(d.Text)
		}
	})
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(strings.SplitN(strings.TrimSpace(b.String()), "\n", 2)[0])
	title = strings.Trim(title, "\"'“”‘’《》「」#* ")
	return truncateRunes(title, 40), nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
