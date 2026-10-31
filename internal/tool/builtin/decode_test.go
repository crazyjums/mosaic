package builtin

import (
	"strings"
	"testing"
)

// 输出里 UTF-8 的行原样保留、其余行交给旧代码页解码：真实场景是 cmd 内置命令的报错是 GBK，而 git / node 的输出是 UTF-8。
func TestDecodeLinesMixed(t *testing.T) {
	// 用假的“旧代码页”：把每个字节 0xC4 0xE3 当作“你”。真正的 GBK 解码只在 Windows 上有，这里只验证分行和保留逻辑。
	legacy := func(b []byte) string { return "【旧】" + strings.TrimRight(string(b), "\r\n") + "\n" }
	in := []byte("utf8 行：你好\n\xc4\xe3\xba\xc3\n最后一行没有换行：中文")
	got := decodeLines(in, legacy)
	want := "utf8 行：你好\n【旧】\xc4\xe3\xba\xc3\n最后一行没有换行：中文"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// 全是合法 UTF-8 时不调用解码函数。
	if got := decodeLines([]byte("hello 你好\n"), func([]byte) string { t.Fatal("不应调用"); return "" }); got != "hello 你好\n" {
		t.Fatalf("%q", got)
	}
	if decodeLines(nil, legacy) != "" {
		t.Fatal("空输入应为空")
	}
}
