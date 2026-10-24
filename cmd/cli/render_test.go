package main

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/event"
)

// 用前端共用的录制事件流驱动终端渲染器，检查关键环节都被渲染出来。
func TestRendererOnRecordedDemo(t *testing.T) {
	f, err := os.Open("../../frontend/src/bridge/demo-events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var out bytes.Buffer
	r := &renderer{out: &out, c: palette{}, echoUser: true}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		ev, err := event.Decode(sc.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		r.handle(ev)
	}
	got := out.String()
	for _, want := range []string{"> 帮我做个演示", "💭 思考", "⚙  调用工具 list_dir", "⚠  需要确认 [write]", "→ allow", "✓  write_file", "🖼", "completed · 4 步"} {
		if !strings.Contains(got, want) {
			t.Errorf("输出中缺少 %q\n%s", want, got)
		}
	}
}
