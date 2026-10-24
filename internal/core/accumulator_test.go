package core

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/crazyjums/myllmapp/internal/provider"
)

func TestAccumulator(t *testing.T) {
	var a accumulator
	for _, d := range []provider.Delta{
		{Type: provider.DeltaThinking, Text: "先"},
		{Type: provider.DeltaThinking, Text: "想想"},
		{Type: provider.DeltaThinkingSignature, Text: "sig1"},
		{Type: provider.DeltaThinking, Text: "再想"}, // 上一个思考块已封口，应开启新块
		{Type: provider.DeltaText, Text: "你"},
		{Type: provider.DeltaText, Text: "好"},
		{Type: provider.DeltaRedactedThinking, Text: "ENC"},
		{Type: provider.DeltaToolCall, ToolCall: &provider.ToolCall{ID: "c1", Name: "t", Input: json.RawMessage(`{}`)}},
		{Type: provider.DeltaUsage, Usage: &provider.Usage{InputTokens: 1}},
		{Type: provider.DeltaUsage, Usage: &provider.Usage{InputTokens: 1, OutputTokens: 9}}, // 以最后一次为准
		{Type: provider.DeltaStop, StopReason: provider.StopToolUse},
	} {
		a.add(d)
	}

	want := []provider.Block{
		{Type: provider.BlockThinking, Text: "先想想", Signature: "sig1"},
		{Type: provider.BlockThinking, Text: "再想"},
		{Type: provider.BlockText, Text: "你好"},
		{Type: provider.BlockRedactedThinking, Data: "ENC"},
		{Type: provider.BlockToolUse, ToolCallID: "c1", ToolName: "t", ToolInput: json.RawMessage(`{}`)},
	}
	if got := a.message().Content; !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks\n got  %+v\n want %+v", got, want)
	}
	if a.usage.OutputTokens != 9 || a.stop != provider.StopToolUse {
		t.Fatalf("usage/stop: %+v %s", a.usage, a.stop)
	}

	partial := salvagePartial(a.message())
	if len(partial.Content) != 1 || partial.Content[0].Type != provider.BlockText {
		t.Fatalf("中断时只应保留文本: %+v", partial.Content)
	}
}
