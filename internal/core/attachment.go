package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// 附件：用户随消息一起发给模型的文件。
//
// 目前支持两类：
//   - 图片（png / jpeg / gif / webp）：作为图片块发给模型，需要模型支持视觉；
//   - 纯文本（Markdown、日志、代码、配置……）：把文件内容放进消息里，模型直接"读到"。
// 以后要支持别的类型（PDF、Office 文档），只需要在这里增加"怎么变成模型能读的块"。
//
// 校验放在内核而不是前端：前端的限制只是体验，内核的限制才是保护（消息会存进数据库、发给厂商）。

// Attachment 是前端交来的一个附件。
type Attachment struct {
	// Name 是文件名（只取最后一段，不含路径）。
	Name string `json:"name"`
	// MediaType 图片必填（image/png 等）；文本可留空。
	MediaType string `json:"media_type,omitempty"`
	// Data 是图片的 base64 内容（不带 data: 前缀）。
	Data string `json:"data,omitempty"`
	// Text 是文本附件的内容。
	Text string `json:"text,omitempty"`
}

// UserInput 是用户的一次发言：文字加附件。
type UserInput struct {
	Text        string
	Attachments []Attachment
}

// 附件的上限。
const (
	MaxAttachments    = 10
	MaxImageBytes     = 5 << 20   // 单张图片解码后 5MB
	MaxTextBytes      = 200 << 10 // 单个文本附件 200KB
	MaxTotalTextBytes = 600 << 10 // 所有文本附件合计 600KB
	maxNameRunes      = 120
)

var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// ErrAttachment 是附件校验失败（文件类型不支持、太大等），消息会说明是哪个文件、为什么。
var ErrAttachment = errors.New("attachment")

func attErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrAttachment, fmt.Sprintf(format, args...))
}

// cleanName 只保留文件名：去掉路径、控制字符，限制长度。名字会放进发给模型的文本里，所以不能带换行之类。
func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		name = "attachment"
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		name = string([]rune(name)[:maxNameRunes])
	}
	return name
}

// fence 返回一个比文本里任何连续反引号都长的围栏，这样文件内容里有 ``` 也不会"逃出"代码块。
func fence(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// buildUserMessage 把用户输入变成一条 user 消息：先是用户打的字，然后是文本附件，最后是图片。
func buildUserMessage(in UserInput) (provider.Message, error) {
	if len(in.Attachments) > MaxAttachments {
		return provider.Message{}, attErr("最多添加 %d 个附件", MaxAttachments)
	}
	var blocks []provider.Block
	if in.Text != "" {
		blocks = append(blocks, provider.Block{Type: provider.BlockText, Text: in.Text})
	}
	var images []provider.Block
	totalText := 0
	for _, a := range in.Attachments {
		name := cleanName(a.Name)
		switch {
		case a.Data != "" || strings.HasPrefix(a.MediaType, "image/"):
			mt := strings.ToLower(a.MediaType)
			if !imageTypes[mt] {
				return provider.Message{}, attErr("%s：不支持的图片格式 %q（支持 PNG / JPEG / GIF / WebP）", name, a.MediaType)
			}
			raw, err := base64.StdEncoding.DecodeString(a.Data)
			if err != nil {
				return provider.Message{}, attErr("%s：图片数据不是合法的 base64", name)
			}
			if len(raw) == 0 {
				return provider.Message{}, attErr("%s：图片是空的", name)
			}
			if len(raw) > MaxImageBytes {
				return provider.Message{}, attErr("%s：图片太大（%d KB，上限 %d MB）", name, len(raw)>>10, MaxImageBytes>>20)
			}
			images = append(images, provider.Block{Type: provider.BlockImage, MediaType: mt, ImageData: a.Data, Attachment: name})
		default:
			if !utf8.ValidString(a.Text) || strings.ContainsRune(a.Text, 0) {
				return provider.Message{}, attErr("%s：不是文本文件（目前只支持 Markdown / 纯文本类文件）", name)
			}
			if len(a.Text) > MaxTextBytes {
				return provider.Message{}, attErr("%s：文件太大（%d KB，单个上限 %d KB）", name, len(a.Text)>>10, MaxTextBytes>>10)
			}
			totalText += len(a.Text)
			if totalText > MaxTotalTextBytes {
				return provider.Message{}, attErr("文本附件合计超过 %d KB", MaxTotalTextBytes>>10)
			}
			f := fence(a.Text)
			body := fmt.Sprintf("[Attached file: %s]\n%s\n%s\n%s", name, f, strings.TrimRight(a.Text, "\n"), f)
			blocks = append(blocks, provider.Block{Type: provider.BlockText, Text: body, Attachment: name})
		}
	}
	blocks = append(blocks, images...)
	if len(blocks) == 0 {
		return provider.Message{}, attErr("消息是空的")
	}
	return provider.Message{Role: provider.RoleUser, Content: blocks}, nil
}

// attachmentInfos 从一条 user 消息里还原出附件摘要，给 user_message 事件用（界面显示附件条）。
// 文本附件只给文件名和大小，不重复发送内容；图片带上数据用来显示缩略图。
func attachmentInfos(m provider.Message) []event.AttachmentInfo {
	var out []event.AttachmentInfo
	for _, b := range m.Content {
		switch {
		case b.Attachment == "":
		case b.Type == provider.BlockImage:
			out = append(out, event.AttachmentInfo{Name: b.Attachment, Kind: "image", MediaType: b.MediaType, Size: base64.StdEncoding.DecodedLen(len(b.ImageData)), Data: b.ImageData})
		case b.Type == provider.BlockText:
			out = append(out, event.AttachmentInfo{Name: b.Attachment, Kind: "text", Size: len(b.Text)})
		}
	}
	return out
}
