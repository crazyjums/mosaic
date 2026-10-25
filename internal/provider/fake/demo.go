package fake

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"runtime"
)

// DemoScript 是内置演示剧本，覆盖第一阶段所有可视化元素：
// 思考卡片、文本流、只读工具（直接执行）、写文件和执行命令（需要确认）、图片、最终回答。
func DemoScript() Script {
	return Script{Steps: []Step{
		{
			Thinking: "用户的请求是：“{{input}}”。我还不了解工作区里有什么，先列一下根目录，这是只读操作，不需要确认。",
			Text:     "我先看一下工作区的目录结构。",
			ToolCalls: []ToolSpec{
				{Name: "list_dir", Input: json.RawMessage(`{"path":"."}`)},
			},
		},
		{
			Thinking: "目录已经看到了。接下来把这次请求记录到一个文件里。写文件属于 write 风险，需要用户确认后才会执行。",
			Text:     "接下来我把记录写入 `agent-demo/hello.md`。",
			ToolCalls: []ToolSpec{
				{Name: "write_file", Input: json.RawMessage(`{"path":"agent-demo/hello.md","content":"# Hello from fake provider\n\n用户输入：{{input}}\n"}`)},
			},
		},
		{
			Thinking: "文件写好了。用 shell 读回来验证一下内容，执行命令属于 exec 风险，同样需要确认。",
			ToolCalls: []ToolSpec{
				{Name: "shell", Input: catCommand("agent-demo/hello.md")},
			},
		},
		{
			Thinking: "所有步骤都完成了，给用户一个总结，并附上一张示意图。",
			Text: "完成了！这次演示经过了以下步骤：\n\n" +
				"1. **list_dir**：列出工作区根目录（只读，自动放行）\n" +
				"2. **write_file**：写入 `agent-demo/hello.md`（需要你确认）\n" +
				"3. **shell**：执行命令读回文件，验证内容（需要你确认）\n\n" +
				"下面是一张由 fake provider 生成的示意图：",
			Image: &ImageSpec{Builtin: "demo"},
		},
	}}
}

// catCommand 生成"打印文件内容"的 shell 命令。shell 工具在 Windows 上用 cmd，没有 cat，要用 type。
func catCommand(path string) json.RawMessage {
	cmd := "cat " + path
	if runtime.GOOS == "windows" {
		cmd = "type " + filepath.FromSlash(path)
	}
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

// demoImage 是启动时用代码画出来的一张小渐变图（base64 PNG）。
// 用代码生成而不是在仓库里放二进制文件，既能演示图片事件，又不增加仓库体积。
var demoImage = func() string {
	const w, h = 160, 90
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(60 + x), G: uint8(80 + y*2), B: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}()
