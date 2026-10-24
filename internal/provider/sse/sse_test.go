package sse

import (
	"io"
	"strings"
	"testing"
)

func TestReader(t *testing.T) {
	in := ": ping\n\nevent: a\ndata: {\"x\":1}\n\ndata:line1\ndata: line2\r\n\nevent: only-name\n\ndata: tail"
	r := NewReader(strings.NewReader(in))
	want := []Event{
		{Name: "a", Data: `{"x":1}`},
		{Data: "line1\nline2"},
		{Data: "tail"},
	}
	for i, w := range want {
		got, err := r.Next()
		if err != nil || got != w {
			t.Fatalf("#%d got %+v %v, want %+v", i, got, err, w)
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("应返回 EOF: %v", err)
	}
}
