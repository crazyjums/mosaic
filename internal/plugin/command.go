package plugin

import (
	"fmt"
	"os"
	"regexp"
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

// placeholderRe 匹配 Claude Code 命令模板里的占位符：$ARGUMENTS 或 $1…$9。
var placeholderRe = regexp.MustCompile(`\$ARGUMENTS|\$[1-9]`)

// renderClaude 按 Claude Code 的写法展开命令：$ARGUMENTS 是用户输入的全部，$1…$9 是按空白拆开（引号内不拆）的第几个参数。
// 没有任何占位符而用户输入了内容时，把内容接在后面，不会丢掉（和原生模板一致）。
//
// 只对**原始模板**扫描一遍：替换进去的用户输入不会再被当成占位符（输入里的 "$5"、"$ARGUMENTS" 保持字面）。
func (t commandTemplate) renderClaude(args string) string {
	args = strings.TrimSpace(args)
	words, err := splitCommand(args)
	if err != nil {
		words = strings.Fields(args)
	}
	used := false
	var b strings.Builder
	last := 0
	for _, loc := range placeholderRe.FindAllStringIndex(t.body, -1) {
		ph := t.body[loc[0]:loc[1]]
		rep := args
		if ph != "$ARGUMENTS" {
			// 只替换单个数字：$10 不是第 10 个参数，也不是 $1 后面跟个 0——保持原样。
			if loc[1] < len(t.body) && t.body[loc[1]] >= '0' && t.body[loc[1]] <= '9' {
				continue
			}
			rep = ""
			if i := int(ph[1] - '1'); i < len(words) {
				rep = words[i]
			}
		}
		used = true
		b.WriteString(t.body[last:loc[0]])
		b.WriteString(rep)
		last = loc[1]
	}
	b.WriteString(t.body[last:])
	if !used && args != "" {
		return b.String() + "\n\n" + args
	}
	return b.String()
}
