package core

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/provider"
)

var pngB64 = base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake"))

func TestBuildUserMessage(t *testing.T) {
	m, err := buildUserMessage(UserInput{
		Text: "看看这些",
		Attachments: []Attachment{
			{Name: `C:\docs\notes.md`, Text: "# 标题\n正文\n"},
			{Name: "shot.png", MediaType: "image/png", Data: pngB64},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Role != provider.RoleUser || len(m.Content) != 3 {
		t.Fatalf("文字 + 文本附件 + 图片: %+v", m.Content)
	}
	if m.Text() != "看看这些" {
		t.Fatalf("Text() 不应包含附件内容: %q", m.Text())
	}
	att := m.Content[1]
	if att.Attachment != "notes.md" || !strings.Contains(att.Text, "[Attached file: notes.md]") || !strings.Contains(att.Text, "# 标题\n正文\n```") {
		t.Fatalf("文本附件应带文件名并放进代码围栏（路径只取文件名）: %q", att.Text)
	}
	if img := m.Content[2]; img.Type != provider.BlockImage || img.Attachment != "shot.png" || img.MediaType != "image/png" {
		t.Fatalf("图片块: %+v", img)
	}
	if !m.IsUserInput() || !m.HasAttachment() {
		t.Fatal("IsUserInput / HasAttachment")
	}
	infos := attachmentInfos(m)
	if len(infos) != 2 || infos[0].Kind != "text" || infos[0].Data != "" || infos[1].Kind != "image" || infos[1].Data != pngB64 {
		t.Fatalf("附件摘要：文本不带内容，图片带数据: %+v", infos)
	}
}

func TestAttachmentOnlyMessageIsATurn(t *testing.T) {
	m, err := buildUserMessage(UserInput{Attachments: []Attachment{{Name: "a.txt", Text: "x"}}})
	if err != nil || m.Text() != "" || !m.IsUserInput() {
		t.Fatalf("只有附件也是一次用户发言: %v %+v", err, m)
	}
	if starts := turnStarts([]provider.Message{m, {Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockText, Text: "ok"}}}}); len(starts) != 1 {
		t.Fatalf("分页应把它算作一轮: %v", starts)
	}
	s := NewSession("s", "p", "m")
	s.appendMessage(m)
	if s.Title != "a.txt" {
		t.Fatalf("只有附件时用文件名做标题: %q", s.Title)
	}
}

func TestAttachmentValidation(t *testing.T) {
	big := strings.Repeat("a", MaxTextBytes+1)
	bigImg := base64.StdEncoding.EncodeToString(make([]byte, MaxImageBytes+1))
	many := make([]Attachment, MaxAttachments+1)
	for i := range many {
		many[i] = Attachment{Name: "a.txt", Text: "x"}
	}
	cases := map[string]UserInput{
		"消息是空的":    {},
		"最多添加":     {Text: "x", Attachments: many},
		"不是文本文件":   {Text: "x", Attachments: []Attachment{{Name: "b.bin", Text: "a\x00b"}}},
		"不是文本文件 2": {Text: "x", Attachments: []Attachment{{Name: "b.bin", Text: "\xff\xfe"}}},
		"文件太大":     {Text: "x", Attachments: []Attachment{{Name: "big.txt", Text: big}}},
		"不支持的图片格式": {Text: "x", Attachments: []Attachment{{Name: "a.svg", MediaType: "image/svg+xml", Data: pngB64}}},
		"不是合法的":    {Text: "x", Attachments: []Attachment{{Name: "a.png", MediaType: "image/png", Data: "!!!"}}},
		"图片太大":     {Text: "x", Attachments: []Attachment{{Name: "a.png", MediaType: "image/png", Data: bigImg}}},
		"文本附件合计超过": {Text: "x", Attachments: []Attachment{{Name: "1", Text: strings.Repeat("a", MaxTextBytes)}, {Name: "2", Text: strings.Repeat("a", MaxTextBytes)}, {Name: "3", Text: strings.Repeat("a", MaxTextBytes)}, {Name: "4", Text: "a"}}},
	}
	for want, in := range cases {
		_, err := buildUserMessage(in)
		if !errors.Is(err, ErrAttachment) || !strings.Contains(err.Error(), strings.Fields(want)[0]) {
			t.Errorf("%s: 应报附件错误，得到 %v", want, err)
		}
	}
}

func TestFenceAndName(t *testing.T) {
	if f := fence("a ``` b"); f != "````" {
		t.Fatalf("围栏要比内容里最长的反引号多一个: %q", f)
	}
	if f := fence("plain"); f != "```" {
		t.Fatalf("默认三个: %q", f)
	}
	for in, want := range map[string]string{"": "attachment", "../../etc/passwd": "passwd", "a\nb.md": "ab.md", `dir\x.md`: "x.md"} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
	// 内容里自带围栏时不能被截断：
	m, _ := buildUserMessage(UserInput{Attachments: []Attachment{{Name: "n.md", Text: "```go\nx\n```"}}})
	if !strings.HasPrefix(m.Content[0].Text, "[Attached file: n.md]\n````\n```go") {
		t.Fatalf("外层围栏应更长: %q", m.Content[0].Text)
	}
}

func TestReplayShowsAttachments(t *testing.T) {
	s := NewSession("s1", "p", "m")
	um, _ := buildUserMessage(UserInput{Text: "总结一下", Attachments: []Attachment{{Name: "n.md", Text: "内容"}}})
	s.appendMessage(um)
	s.appendMessage(provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockText, Text: "好的"}}})
	var got []string
	for _, ev := range ReplayEvents(s, nil) {
		if um, ok := ev.Data.(event.UserMessage); ok {
			got = append(got, um.Text)
			if len(um.Attachments) != 1 || um.Attachments[0].Name != "n.md" {
				t.Fatalf("重放的用户消息应带附件摘要: %+v", um)
			}
		}
	}
	if len(got) != 1 || got[0] != "总结一下" {
		t.Fatalf("用户气泡里只有用户打的字: %v", got)
	}
}
