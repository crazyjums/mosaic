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

var positionalRe = regexp.MustCompile(`\$([1-9])`)

// renderClaude 按 Claude Code 的写法展开命令：$ARGUMENTS 是用户输入的全部，$1…$9 是按空白拆开（引号内不拆）的第几个参数。
// 没有任何占位符而用户输入了内容时，把内容接在后面，不会丢掉（和原生模板一致）。
func (t commandTemplate) renderClaude(args string) string {
	args = strings.TrimSpace(args)
	words, err := splitCommand(args)
	if err != nil {
		words = strings.Fields(args)
	}
	used := strings.Contains(t.body, "$ARGUMENTS")
	out := strings.ReplaceAll(t.body, "$ARGUMENTS", args)
	// 只替换单个数字：$10 不是第 10 个参数，也不是 $1 后面跟个 0——保持原样。
	out = replaceAllSubmatchFunc(positionalRe, out, func(m []string, next byte) (string, bool) {
		if next >= '0' && next <= '9' {
			return "", false
		}
		used = true
		i := int(m[1][0] - '1')
		if i < len(words) {
			return words[i], true
		}
		return "", true
	})
	if !used && args != "" {
		return out + "\n\n" + args
	}
	return out
}

// replaceAllSubmatchFunc 类似 ReplaceAllStringFunc，但回调能看到匹配后面的那个字符；返回 false 表示这一处不替换。
func replaceAllSubmatchFunc(re *regexp.Regexp, s string, f func(m []string, next byte) (string, bool)) string {
	var b strings.Builder
	last := 0
	for _, loc := range re.FindAllStringSubmatchIndex(s, -1) {
		var next byte
		if loc[1] < len(s) {
			next = s[loc[1]]
		}
		m := []string{s[loc[0]:loc[1]], s[loc[2]:loc[3]]}
		if rep, ok := f(m, next); ok {
			b.WriteString(s[last:loc[0]])
			b.WriteString(rep)
			last = loc[1]
		}
	}
	b.WriteString(s[last:])
	return b.String()
}
