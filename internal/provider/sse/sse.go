package sse

import (
	"bufio"
	"io"
	"strings"
)

// Event 是一个 SSE 事件。
type Event struct {
	Name string // "event:" 字段，可能为空（OpenAI 就不发 event 名）
	Data string // 多行 "data:" 按规范用 \n 连接
}

// Reader 从流中逐个读取 SSE 事件。
type Reader struct {
	sc *bufio.Scanner
}

func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	// 默认单行上限 64KB；一个 data 行可能包含很大的 JSON（例如整段工具参数或 base64 图片），放宽到 16MB。
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return &Reader{sc: sc}
}

// Next 返回下一个事件。流结束时返回 io.EOF。
//
// 解析规则（WHATWG SSE 规范的子集）：
//   - 以 ":" 开头的行是注释（常用作心跳），忽略；
//   - "field: value" 或 "field:value"，value 前最多去掉一个空格；
//   - 空行表示一个事件结束；没有 data 的事件被跳过。
func (r *Reader) Next() (Event, error) {
	var ev Event
	var data []string
	for r.sc.Scan() {
		line := strings.TrimRight(r.sc.Text(), "\r")
		if line == "" {
			if len(data) > 0 {
				ev.Data = strings.Join(data, "\n")
				return ev, nil
			}
			ev = Event{}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev.Name = value
		case "data":
			data = append(data, value)
		}
	}
	if err := r.sc.Err(); err != nil {
		return Event{}, err
	}
	// 流结束时可能还有一个没以空行结尾的事件。
	if len(data) > 0 {
		ev.Data = strings.Join(data, "\n")
		return ev, nil
	}
	return Event{}, io.EOF
}
