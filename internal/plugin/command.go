package plugin

import (
	"fmt"
	"os"
	"strings"
)

// 斜杠命令的模板是一个 markdown 文件：开头可以有 --- 包起来的前置块（目前只认 description），
// 正文里的 {{args}} 在使用时换成用户在命令后面输入的内容。
//
//	---
//	description: 总结一篇飞书文档
//	---
//	请阅读 {{args}} 指向的文档，总结要点。

type commandTemplate struct {
	description string
	body        string
}

func loadCommandTemplate(path string) (commandTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return commandTemplate{}, err
	}
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	t := commandTemplate{body: text}
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if head, body, ok := strings.Cut(rest, "\n---"); ok {
			t.body = strings.TrimPrefix(body, "\n")
			for _, line := range strings.Split(head, "\n") {
				if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "description" {
					t.description = strings.Trim(strings.TrimSpace(v), `"'`)
				}
			}
		}
	}
	t.body = strings.TrimSpace(t.body)
	if t.body == "" {
		return t, fmt.Errorf("命令模板是空的")
	}
	return t, nil
}

// render 展开模板。模板里没有 {{args}} 而用户输入了内容时，把内容接在后面，不会丢掉。
func (t commandTemplate) render(args string) string {
	args = strings.TrimSpace(args)
	if strings.Contains(t.body, "{{args}}") {
		return strings.ReplaceAll(t.body, "{{args}}", args)
	}
	if args == "" {
		return t.body
	}
	return t.body + "\n\n" + args
}
