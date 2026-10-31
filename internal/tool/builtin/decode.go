package builtin

import (
	"bytes"
	"unicode/utf8"
)

// decodeLines 把可能混着两种编码的输出转成 UTF-8：每一行如果本来就是合法 UTF-8 就原样保留，
// 否则交给 fromLegacy 按系统的旧代码页解码。放在与平台无关的文件里，是为了能在任何系统上测试。
func decodeLines(b []byte, fromLegacy func([]byte) string) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out bytes.Buffer
	for len(b) > 0 {
		line := b
		rest := []byte(nil)
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, rest = b[:i+1], b[i+1:]
		}
		if utf8.Valid(line) {
			out.Write(line)
		} else {
			out.WriteString(fromLegacy(line))
		}
		b = rest
	}
	return out.String()
}
